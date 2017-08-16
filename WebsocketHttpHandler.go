package main

import (
  "net/http"
  "net"
  "fmt"
  "strings"
  "time"
  "encoding/json"
)

func (ws *Wsocket) http_home(w http.ResponseWriter, r *http.Request) {
  w.Write([]byte("working."))
}

func (ws *Wsocket) http_timelock(w http.ResponseWriter, r *http.Request) {
  var result LockResult

  auth := r.Header.Get("AuthToken")
  result.SN = SafeParams(r, "sn")
  result.Cmd = SafeParams(r, "cmd")
  result.Lock_begin = SafeParams(r, "lock_begin")
  result.Lock_end = SafeParams(r, "lock_end")
  result.BossName = SafeParams(r, "bossname")
  result.BossID = SafeParams(r, "bossid")
  ip, _, _ := net.SplitHostPort(r.RemoteAddr)
  forwarded := r.Header.Get("X-FORWARDED-FOR")

  clientInfo := fmt.Sprintf("[remote:%s][forwarded:%s][auth:%s]", ip, forwarded, auth)
  fmt.Println("HandleLockTimePeriod start", result, clientInfo)

  //send to app
  info, ok := service.connMgr.GetConnInfoByHardwareID(result.SN)
  if !ok {
    result.Status = ERR_NOT_ONLINE
  } else if strings.Compare(auth, GetConfigInstance().WebsocketAuthToken) == 0 {
    if result.Cmd == WS_MSG_LOCK || result.Cmd == WS_MSG_UNLOCK { //锁定
      if err := service.SendLockTimePeriod(info.conn, result); err != nil { //发给app
        result.Status = ERR_SEND_APP_FAILED
      } else {
        //wait response
        select {
        case <-time.After(time.Duration(3) * time.Second):
          result.Status = ERR_APP_RESPONE_TIMEOUT
        case status := <-info.timelockStatus:
          result.LockStatus = status
          result.Status = int(STATUS_OK) //回复时间段锁定已经成功
        }
      }
    } else {
      result.Status = ERR_PARAMS_ERR
    }
  } else {
    result.Status = ERR_NOT_AUTH
  }
  fmt.Println("HandleLockTimePeriod end", result, clientInfo)

  b, _ := json.Marshal(&result)
  w.Header().Set("Content-Type", "application/json")
  w.Write(b)
}

func (ws *Wsocket) http_lock(w http.ResponseWriter, r *http.Request) {
  auth       := r.Header.Get("AuthToken")
  sn         := SafeParams(r, "sn")
  cmd        := SafeParams(r, "cmd")
  bossid     := SafeParams(r, "bossid")

  //auth
  if strings.Compare(auth, GetConfigInstance().WebsocketAuthToken) != 0 {
    return
  }

  var resp LockResult
  resp.Cmd = cmd
  if cmd == WS_MSG_LOCK {
    resp.LockStatus = 1
  } else {
    resp.LockStatus = 0
  }

  info, ok := service.connMgr.GetConnInfoByHardwareID(sn)
  if !ok {
    resp.Status = ERR_NOT_ONLINE
  } else {
    p := packLockPackage(resp.LockStatus, sn)
    _, err := info.conn.Write(p) //发送给app
    if err != nil {
      fmt.Println("SendManagerCmd failed", cmd, sn, err)
      resp.Status = ERR_SEND_APP_FAILED
    }
    select {
    case <-time.After(time.Duration(3) * time.Second):
      resp.Status = ERR_APP_RESPONE_TIMEOUT
    case rec := <-info.lockStatus:
      rec = rec
      resp.Status = int(STATUS_OK)
    }
  }

  //2. send to web
  ws.send_hardware_msg(bossid, sn, cmd)

  //3. responce to the http
  b, _ := json.Marshal(&resp)
  w.Header().Set("Content-Type", "application/json")
  w.Write(b)
}
