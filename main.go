package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/getlantern/systray"
)

var CurrentPort = "8080"

func main() {
	LoadConfig()

	if len(os.Args) > 1 {
		CurrentPort = os.Args[1]
		AppCfg.Port = CurrentPort
		SaveConfig()
	} else {
		CurrentPort = AppCfg.Port
	}

	go StartWebServer()
	systray.Run(onReady, onExit)
}

func onReady() {
	exePath, err := os.Executable()
	if err == nil {
		iconPath := filepath.Join(filepath.Dir(exePath), "icon.ico")
		if iconBytes, err := os.ReadFile(iconPath); err == nil && len(iconBytes) > 0 {
			systray.SetIcon(iconBytes)
		} else {
			systray.SetIcon([]byte{0, 0, 1, 0, 1, 0, 16, 16, 0, 0, 1, 0, 32, 0, 104, 0, 0, 0, 22, 0, 0, 0, 40, 0, 0, 0, 16, 0, 0, 0, 32, 0, 0, 0, 1, 0, 32, 0, 0, 0, 0, 0, 64, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 255, 255, 255, 255})
		}
	}

	systray.SetTitle("PCparamWEB")
	systray.SetTooltip("PCparamWEB Monitor")

	mPort := systray.AddMenuItem("PCparamWEB", "Кликните для просмотра подключений и смены порта")
	mConfig := systray.AddMenuItem("Config", "Открыть веб-интерфейс настроек в браузере")
	systray.AddSeparator()
	mExit := systray.AddMenuItem("Exit", "Выход из программы")

	go func() {
		for {
			<-mPort.ClickedCh

			ClientsMu.Lock()
			now := time.Now()
			var clientLines []string
			for ip, c := range ActiveClients {
				if now.Sub(c.LastSeen) < 30*time.Second {
					if c.Name != "" && c.Name != ip {
						clientLines = append(clientLines, fmt.Sprintf("• %s (%s)", c.Name, ip))
					} else {
						clientLines = append(clientLines, fmt.Sprintf("• %s", ip))
					}
				}
			}
			ClientsMu.Unlock()

			devicesStr := "Нет активных подключений"
			if len(clientLines) > 0 {
				devicesStr = strings.Join(clientLines, "\n")
			}

			msgText := fmt.Sprintf("Текущий порт: %s\n\nПодключенные устройства:\n%s\n\nДля смены порта введите новое значение:", CurrentPort, devicesStr)

			psScript := `
			Add-Type -AssemblyName Microsoft.VisualBasic
			$port = [Microsoft.VisualBasic.Interaction]::InputBox('` + msgText + `', 'PCparamWEB — Управление', '` + CurrentPort + `')
			if ($port -match '^\d+$') { [System.IO.File]::WriteAllText("$env:TEMP\pc_port.tmp", $port) }
			`
			cmd := exec.Command("powershell", "-NoProfile", "-WindowStyle", "Hidden", "-Command", psScript)
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			cmd.Run()

			tempFile := filepath.Join(os.TempDir(), "pc_port.tmp")
			if data, err := os.ReadFile(tempFile); err == nil {
				newPort := strings.TrimSpace(string(data))
				os.Remove(tempFile)
				if newPort != "" && newPort != CurrentPort {
					CurrentPort = newPort
					SaveConfig()
					systray.Quit()
					exec.Command(os.Args[0], newPort).Start()
					os.Exit(0)
				}
			}
		}
	}()

	go func() {
		for {
			<-mConfig.ClickedCh
			url := fmt.Sprintf("http://localhost:%s", CurrentPort)
			var cmd *exec.Cmd
			switch runtime.GOOS {
			case "windows":
				cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
			case "darwin":
				cmd = exec.Command("open", url)
			default:
				cmd = exec.Command("xdg-open", url)
			}
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			cmd.Run()
		}
	}()

	go func() {
		<-mExit.ClickedCh
		systray.Quit()
		os.Exit(0)
	}()
}

func onExit() {}
