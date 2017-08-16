package main

import (
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"net/http"
	"time"
  "strconv"
  "errors"
	"sync"
  "github.com/Jeffail/gabs"
)

type Wsocket struct {
	stop           chan bool
	wg             sync.WaitGroup

	connectionsMap map[string][]*websocket.Conn
	connLock       sync.Mutex

  wsWriteLock    sync.Mutex
}

type BasePacket struct {
  Type    string           `json:"type"`
  Data    json.RawMessage  `json:"data"`
  Cookie  *string          `json:"cookie"`
}

func NewWsocket() *Wsocket {
	return &Wsocket{
		stop:           make(chan bool),
		wg:             sync.WaitGroup{},
		connectionsMap: make(map[string][]*websocket.Conn, 1000), //同时支持1000个boss在线监控
	}
}

func (ws *Wsocket) Start() {
	fmt.Println("websocket listening in port", GetConfigInstance().WebsocketPort)
	go ws.webSocket()
}

func (ws *Wsocket) webSocket() {
	http.HandleFunc("/echo", ws.websocket_stub)

	http.HandleFunc("/", ws.http_home)
	http.HandleFunc("/locktime", ws.http_timelock)
	http.HandleFunc("/lock", ws.http_lock)

	service := fmt.Sprintf("0.0.0.0:%d", GetConfigInstance().WebsocketPort)
	http.ListenAndServe(service, nil)
}

func (ws *Wsocket) InitDroneInfo(c *websocket.Conn, uid string) error {
  var lockedInfo LockedInfo
  var err error

  if uid == _SUPER_UID_ {
    //如果登录者是超级用户
    lockedInfo, err = service.dbMgr.GetSuperDroneLockedInfo(uid)
  } else {
    //构建websocket初始化信息：locked数量，drone总的数量
    lockedInfo, err = service.dbMgr.GetDroneLockedInfo(uid)
  }

  if err != nil {
    fmt.Println("init information error", err)
    return err
  }

  var init_info struct {
    Type string     `json:"type"`
    Data LockedInfo `json:"data"`
  }
  init_info.Data.Drone_sum = lockedInfo.Drone_sum
  init_info.Data.Locked_drones = lockedInfo.Locked_drones
  init_info.Type = INIT
  err = ws.SafeWriteJSON(c, init_info)

  if err != nil {
    fmt.Println("write json init info failed", uid, err)
    return err
  }

  return nil
}

func check_login(uid string, cookie string, addr string) (bool, error) {
  if uid == "" || cookie == "" {
    return false, errors.New("null parameters")
  }

  token, err := service.djiGateWay.GetTokenByCookie(cookie, addr)
  if err != nil || len(token) == 0 {
    return false, err
  }

  userInfo, err := service.djiGateWay.CheckToken(string(token), addr)
  if err != nil || userInfo.Status != 0 || len(userInfo.Items) == 0 || userInfo.Items[0].Item.UserID == 0 {
    fmt.Println("app auth failed", cookie, err, userInfo)
    return false, err
  }

  if uid == strconv.FormatUint(userInfo.Items[0].Item.UserID, 10) {
    return true, nil
  }

  return false, errors.New("invalid uid")
}

func (ws *Wsocket) SafeMapSet(k string, v *websocket.Conn) {
  ws.connLock.Lock()
  defer ws.connLock.Unlock()

	if _, ok := ws.connectionsMap[k]; !ok {
		ws.connectionsMap[k] = make([]*websocket.Conn, 0, 10)
	}
	ws.connectionsMap[k] = append(ws.connectionsMap[k], v)
}

func (ws *Wsocket) SafeMapDel(k string, c *websocket.Conn) {
	ws.connLock.Lock()
  defer ws.connLock.Unlock()

  ws.MapDel(k, c)
}

func (ws *Wsocket) MapDel(k string, c *websocket.Conn) {
  for n, v := range ws.connectionsMap[k] {
    if v == c {
      //https://github.com/golang/go/wiki/SliceTricks
      copy(ws.connectionsMap[k][n:], ws.connectionsMap[k][n+1:])
      ws.connectionsMap[k][len(ws.connectionsMap[k])-1] = nil // or the zero value of T
      ws.connectionsMap[k] = ws.connectionsMap[k][:len(ws.connectionsMap[k])-1]
      break
    }
  }

  if len(ws.connectionsMap[k]) == 0 {
    delete(ws.connectionsMap, k)
  }
}

func (ws *Wsocket) SafeWriteJSON(c *websocket.Conn, json interface{}) error {
  ws.wsWriteLock.Lock()
  defer ws.wsWriteLock.Unlock()

	return c.WriteJSON(json)
}

func (ws *Wsocket) wsCheckIsBoss(sn string, uid string) bool {
	tmpid, _ := service.dbMgr.GetBossIDBySn(sn)
	if uid == tmpid {
		return true
	}
	return false
}

func (ws *Wsocket) send_hardware_msg(uid string, sn string, cmd string) {
  ws.connLock.Lock()
  ws.wsWriteLock.Lock()

  defer func() {
    ws.connLock.Unlock()
    ws.wsWriteLock.Unlock()
  }()

  conns, ok := ws.connectionsMap[uid]
  if !ok {
    return
  }

  ret := gabs.New()
  ret.Set(cmd, "type")
  ret.SetP(sn, "data.machine_info.hardware_id")

  for _, conn := range conns {
    conn.WriteJSON(ret.Data())
  }
}

func (ws *Wsocket) WsocketSendIuavFlightData(uid string, flyer FlyerInfo, info IuavFlightData) {
  ws.connLock.Lock()
  ws.wsWriteLock.Lock()

  defer func() {
    ws.connLock.Unlock()
    ws.wsWriteLock.Unlock()
  }()

  v, ok := ws.connectionsMap[uid]
  if !ok {
    return
  }

  var gps_info GpsInfo
  var point Point

  if info.FrameFlag == 0 {
    gps_info.Type = ONLINE
  } else {
    gps_info.Type = OFFLINE
  }

  if now := time.Now().Unix() * 1000; info.Timestamp+DelayTime < uint64(now) {
    //处理缓存数据是否需要实时显示问题
    return
  }

  gps_info.Point_data.Machine_info.Hardware_id = flyer.HardwareSN
  gps_info.Point_data.Machine_info.Nickname = flyer.HardwareName
  gps_info.Point_data.Machine_info.Type = flyer.HardwareType
  gps_info.Point_data.Machine_info.Locked = flyer.HardwareLocked

  gps_info.Point_data.Flyer_info.Uid = flyer.UserID
  gps_info.Point_data.Flyer_info.Realname = flyer.UserName
  gps_info.Point_data.Flyer_info.Avatar = ""
  gps_info.Point_data.Flyer_info.Job_level = flyer.Job_level

  gps_info.Point_data.Flyer_info.Team_info.Id = flyer.TeamID
  gps_info.Point_data.Flyer_info.Team_info.Name = flyer.TeamName

	point.Lati = info.Lati
	point.Longi = info.Longi
	point.Plant = info.Plant
	point.Velocity_x = info.VelocityX
	point.Velocity_y = info.VelocityY
	point.Radar_height = info.RadarHeight
	point.Farm_delta_y = info.FarmDeltaY
	point.WorkArea = uint32(info.WorkArea) //增加已喷面积 2017.01.01 14:18
  flyer.TodayWork += uint32(info.WorkArea)
	point.TodayWork = flyer.TodayWork //增加当天作业总亩数
	point.FlowSpeed = info.FlowSpeed //协议1.1新增字段
  gps_info.Point_data.Point_info = point

  for _, c := range v {
    err := c.WriteJSON(gps_info)
    if err != nil {
      ws.MapDel(flyer.BossID, c)
    }
  }
  v = nil
}
