package main

import (
  "net/http"
  "fmt"
  "github.com/gorilla/websocket"
  "errors"
  "encoding/json"
  "time"
)

type ws_protocol_handler func (*string, BasePacket, *websocket.Conn) error

var ws_protocol_handler_dispatch_table = map[string]ws_protocol_handler {
  WS_MSG_UID:      ws_msg_uid_handler,
  WS_MSG_LOCK:     ws_msg_lock_handler,
  WS_MSG_UNLOCK:   ws_msg_lock_handler,
  WS_MSG_SENDMSG:  ws_msg_sendmsg_handler,
}

var upgrader = websocket.Upgrader{
  ReadBufferSize:    1024,
  WriteBufferSize:   1024,
  EnableCompression: true,
  CheckOrigin:       func(r *http.Request) bool { return true },
}

func (ws *Wsocket) websocket_stub(w http.ResponseWriter, r *http.Request) {
  c, err := upgrader.Upgrade(w, r, nil)
  if err != nil {
    fmt.Println("websocket echo error", err)
    return
  }

  go websocket_msg_loop(c)
}

func websocket_msg_loop(c *websocket.Conn) {
  var conn_binding_uid string

  defer func() {
    fmt.Println("websocket close connection")
    c.WriteMessage(websocket.CloseMessage, []byte{})
    c.Close()

    if conn_binding_uid != "" {
      fmt.Println("websocket del connection info")
      ws.SafeMapDel(conn_binding_uid, c)
    }
  }()

  var msg BasePacket
  for {
    if err := c.ReadJSON(&msg); err != nil {
      fmt.Println("websocket read json error", err)
      return
    }

    handler, ok := ws_protocol_handler_dispatch_table[msg.Type]
    if ok {
      err := handler(&conn_binding_uid, msg, c)
      if err != nil {
        fmt.Println("process websocket msg '", msg.Type, "'error: ", err)
      }
    } else {
      fmt.Println("websocket unknown msg type", msg.Type)
    }
  }
}

func ws_msg_uid_handler(binding_uid *string, msg BasePacket, conn *websocket.Conn) error {
  uid := string(msg.Data)

  uid_len := len(uid)
  if uid_len <= 2 {
    return errors.New("wrong uid length")
  }

  if msg.Cookie == nil {
    return errors.New("empty cookie")
  }

  uid = uid[1 : uid_len - 1]

  if result, err := check_login(uid, *msg.Cookie, conn.RemoteAddr().String()); !result {
    *binding_uid = ""
    fmt.Println("websocket check login failed", err)
    return err
  }

  err := ws.InitDroneInfo(conn, uid)
  if err != nil {
    return err
  }
  ws.SafeMapSet(uid, conn)

  *binding_uid = uid

  return nil
}

func (ws *Wsocket) notify_app_wait_response(c *websocket.Conn, sn string, cmd uint8) {
  var responce RsponceResult
  responce.Type = RESPONCE

  if cmd == LOCKED {
    responce.Data.Type = WS_MSG_LOCK
  } else if cmd == UNLOCKED {
    responce.Data.Type = WS_MSG_UNLOCK
  }
  responce.Data.HardwareId = sn

  info, ok := service.connMgr.GetConnInfoByHardwareID(sn)

  if !ok {
    responce.Data.Status = ERR_NOT_ONLINE
  } else {
    p := packLockPackage(cmd, sn)

    _, err := info.conn.Write(p)
    if err != nil {
      fmt.Println("SendManagerCmd failed", cmd, sn, err)
      responce.Data.Status = ERR_SEND_APP_FAILED
    }

    select {
    case <-time.After(time.Duration(3) * time.Second):
      responce.Data.Status = ERR_APP_RESPONE_TIMEOUT
    case status := <-info.lockStatus:
      if status == 1 { //锁定成功
        responce.Data.Status = uint(STATUS_OK) //给网页发送的状态码统一使用0表示成功

        service.dbMgr.dbmap.Exec("update agro_active_info set locked=1,locked_notice=1 where hardware_id=?", sn)
      } else {
        responce.Data.Status = ERR_SEND_APP_FAILED
      }
    }
  }

  ws.SafeWriteJSON(c, responce)
}

func ws_msg_lock_handler(binding_uid *string, msg BasePacket, conn *websocket.Conn) error {
  if *binding_uid == "" {
    return errors.New("websocket lock msg handler no binding uid")
  }

  var lock_msg struct {
    HardwareID string  `json:"hardware_id"`
  }

  err := json.Unmarshal(msg.Data, lock_msg)
  if err != nil {
    return err
  }

  if len(lock_msg.HardwareID) <= 0 {
    return errors.New("websocket lock msg handler empty hardware id")
  }

  if ok := ws.wsCheckIsBoss(lock_msg.HardwareID, *binding_uid); !ok {
    return errors.New("websocket lock msg handler no privilege")
  }

  ws.notify_app_wait_response(conn, lock_msg.HardwareID, LOCKED)

  return nil
}

func (ws *Wsocket) broadcast_to_all_boss_drones(c *websocket.Conn, msg string, bossID string) {
  drones, _ := service.dbMgr.QueryOnlineDroneInfo(bossID) //查找出所有在线飞行器
  for _, v := range drones {
    go ws.send_msg(c, v, msg)
  }
}

func (ws *Wsocket) send_msg(c *websocket.Conn, sn string, msg string) {
  var responce RsponceMsg
  responce.Type = RESPONCE
  responce.Data.Type = WS_MSG_SENDMSG
  responce.Data.HardwareId = sn

  info, ok := service.connMgr.GetConnInfoByHardwareID(sn)
  if !ok {
    responce.Data.Status = ERR_NOT_ONLINE
  } else {
    p := packMsgPackage(msg, sn)

    _, err := info.conn.Write(p)
    if err != nil {
      fmt.Println("SendManagerMsg failed", msg, sn, err)
      responce.Data.Status = ERR_SEND_APP_FAILED
    }

    select {
    case <-time.After(time.Duration(3) * time.Second):
      responce.Data.Status = ERR_APP_RESPONE_TIMEOUT
    case status := <-info.msgStatus:
      if status == 0 {
        responce.Data.Status = uint(STATUS_OK)
        responce.Data.Msg_sum = 100
      } else {
        responce.Data.Status = ERR_SEND_APP_FAILED
      }
    }
  }

  ws.SafeWriteJSON(c, responce)
}

func ws_msg_sendmsg_handler(binding_uid *string, msg BasePacket, conn *websocket.Conn) error {
  if *binding_uid == "" {
    return errors.New("websocket lock msg handler no binding uid")
  }

  var sendmsg_msg struct {
    HardwareID   string `json:"hardware_id"`
    Content      string `json:"content"`
    SendToAll    bool   `json:"send_to_all"`
  }

  err := json.Unmarshal(msg.Data, sendmsg_msg)
  if err != nil {
    return err
  }

  if ok := ws.wsCheckIsBoss(sendmsg_msg.HardwareID, *binding_uid); !ok {
    return errors.New("websocket sendmsg msg handler no privilege")
  }

  if sendmsg_msg.SendToAll {
    ws.broadcast_to_all_boss_drones(conn, sendmsg_msg.Content, *binding_uid)
  } else {
    ws.send_msg(conn, sendmsg_msg.Content, *binding_uid)
  }

  return nil
}
