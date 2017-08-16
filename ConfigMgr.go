package main

import (
  "io/ioutil"
  "gopkg.in/yaml.v2"
)

type GlobalConfig struct {
  PublicPemPath          string   `yaml:"public_pem_path"`
  PrivatePemPath         string   `yaml:"private_pem_path"`

  ServerPort             uint16   `yaml:"server_port"`

  WebsocketPort          uint16   `yaml:"websocket_port"`

  WebsocketAuthToken     string   `yaml:"websocket_auth_token"`

  Gateway                string   `yaml:"gateway"`
  GatewayAppID           string   `yaml:"gateway_app_id"`
  GatewayAppKey          string   `yaml:"gateway_app_key"`

  Database               string   `yaml:"database"`
}

type GlobalConfigs struct {
  Env       string                    `yaml:"env"`
  Configs   map[string]GlobalConfig
}

var instance GlobalConfigs

func GetConfigInstance() GlobalConfig {
  return instance.Configs[instance.Env]
}

func (config GlobalConfig) ENV() string {
  return instance.Env
}

func initConfig() error {
  buf, err := ioutil.ReadFile("config.yaml")

  if err != nil {
    return err
  }

  err = yaml.Unmarshal(buf, &instance)
  if err != nil {
    return err
  }

  return nil
}
