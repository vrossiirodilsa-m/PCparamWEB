package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type ConnectedClient struct {
	IP       string
	Name     string
	LastSeen time.Time
}

type AppConfig struct {
	Port         string `json:"port"`
	Theme        string `json:"theme"`
	ShowCPULoad  bool   `json:"show_cpu_load"`
	ShowCPUTemp  bool   `json:"show_cpu_temp"`
	ShowRAMGraph bool   `json:"show_ram_graph"`
	ShowGPULoad  bool   `json:"show_gpu_load"`
	ShowGPUTemp  bool   `json:"show_gpu_temp"`
	ShowNETGraph bool   `json:"show_net_graph"`
}

var (
	ConfigFile    = "config.json"
	ClientsMu     sync.Mutex
	ActiveClients = make(map[string]ConnectedClient)
	AppCfg        = AppConfig{
		Port:         "8080",
		Theme:        "dark",
		ShowCPULoad:  true,
		ShowCPUTemp:  true,
		ShowRAMGraph: true,
		ShowGPULoad:  true,
		ShowGPUTemp:  true,
		ShowNETGraph: true,
	}
	CfgMu sync.Mutex
)

func LoadConfig() {
	CfgMu.Lock()
	defer CfgMu.Unlock()
	exePath, err := os.Executable()
	if err == nil {
		cfgPath := filepath.Join(filepath.Dir(exePath), ConfigFile)
		if data, err := os.ReadFile(cfgPath); err == nil {
			_ = json.Unmarshal(data, &AppCfg)
			if AppCfg.Port != "" {
				CurrentPort = AppCfg.Port
			}
		}
	}
}

func SaveConfig() {
	CfgMu.Lock()
	defer CfgMu.Unlock()
	AppCfg.Port = CurrentPort
	exePath, err := os.Executable()
	if err == nil {
		cfgPath := filepath.Join(filepath.Dir(exePath), ConfigFile)
		if data, err := json.MarshalIndent(AppCfg, "", "  "); err == nil {
			_ = os.WriteFile(cfgPath, data, 0644)
		}
	}
}
