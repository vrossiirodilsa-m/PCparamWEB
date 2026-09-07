package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/getlantern/systray"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
	gopsnet "github.com/shirou/gopsutil/v3/net"
)

type ConnectedClient struct {
	IP       string
	Name     string
	LastSeen time.Time
}

var (
	currentPort   = "8080"
	portFile      = "port.cfg"
	clientsMu     sync.Mutex
	activeClients = make(map[string]ConnectedClient)

	// Переменные для расчета сетевой скорости
	lastNetBytesRecv uint64
	lastNetBytesSent uint64
	lastNetCheckTime time.Time
	netSpeedDown     float64
	netSpeedUp       float64
)

func main() {
	if len(os.Args) > 1 {
		currentPort = os.Args[1]
	} else {
		exePath, err := os.Executable()
		if err == nil {
			cfgPath := filepath.Join(filepath.Dir(exePath), portFile)
			if data, err := os.ReadFile(cfgPath); err == nil {
				savedPort := strings.TrimSpace(string(data))
				if savedPort != "" {
					currentPort = savedPort
				}
			}
		}
	}

	go startWebServer()
	systray.Run(onReady, onExit)
}

func savePortConfig(port string) {
	exePath, err := os.Executable()
	if err == nil {
		cfgPath := filepath.Join(filepath.Dir(exePath), portFile)
		os.WriteFile(cfgPath, []byte(port), 0644)
	}
}

func startWebServer() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleIndex)
	mux.HandleFunc("/api/stats", handleStats)

	for {
		addr := ":" + currentPort
		log.Printf("Starting server on http://localhost%s", addr)
		err := http.ListenAndServe(addr, mux)
		if err != nil {
			log.Printf("Port %s busy or error: %v. Retrying...", currentPort, err)
			time.Sleep(2 * time.Second)
		}
	}
}

func trackClient(r *http.Request) {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}

	if ip == "127.0.0.1" || ip == "::1" {
		return
	}

	customName := r.URL.Query().Get("name")

	clientsMu.Lock()
	defer clientsMu.Unlock()

	client, exists := activeClients[ip]
	client.IP = ip
	client.LastSeen = time.Now()

	if customName != "" {
		client.Name = customName
	}

	if !exists {
		if customName == "" {
			client.Name = ""
		}
		activeClients[ip] = client

		if customName == "" {
			go func(targetIP string) {
				script := fmt.Sprintf(`
				$name = $null
				try { $name = [System.Net.Dns]::GetHostByAddress('%s').HostName } catch {}
				if (-not $name) {
					$nbt = nbtstat -A '%s' 2>$null
					foreach($line in $nbt) {
						if($line -match '<00>\s+UNIQUE') { $name = $line.Trim().Split()[0]; break }
					}
				}
				if ($name) { echo $name }
				`, targetIP, targetIP)

				cmd := exec.Command("powershell", "-NoProfile", "-Command", script)
				cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
				out, err := cmd.Output()
				if err == nil {
					resolved := strings.TrimSpace(string(out))
					if resolved != "" && resolved != targetIP {
						clientsMu.Lock()
						if c, ok := activeClients[targetIP]; ok {
							c.Name = resolved
							activeClients[targetIP] = c
						}
						clientsMu.Unlock()
					}
				}
			}(ip)
		}
	} else {
		activeClients[ip] = client
	}
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	trackClient(r)
	html := `<!DOCTYPE html>
<html lang="ru">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>PCparamWEB</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
            background: #000000;
            color: #aaaaaa;
            margin: 0;
            padding: 4px 8px;
            display: flex;
            flex-direction: column;
            align-items: flex-start;
            justify-content: flex-start;
            height: 100vh;
            box-sizing: border-box;
        }
        .header-container {
            width: 100%;
            text-align: center;
        }
        h1 {
            font-size: 0.75rem;
            color: #777777;
            margin: 0 0 4px 0;
            font-weight: normal;
        }
        .stats-line {
            font-size: 1.65rem;
            color: #aaaaaa;
            font-weight: bold;
            white-space: pre;
            text-align: left;
            width: 100%;
            margin-top: 2px;
        }
    </style>
</head>
<body>
    <div class="header-container">
        <h1>PCparamWEB</h1>
    </div>
    <div class="stats-line" id="cpu-line">CPU:   -- %    -- ˚С</div>
    <div class="stats-line" id="ram-line">RAM:   -- %    -- ГБ</div>
    <div class="stats-line" id="gpu-line">GPU:   -- %    -- ˚С</div>
    <div class="stats-line" id="net-line">NET:   ↓ --      ↑ --</div>
    <script>
        function updateStats() {
            fetch('/api/stats')
                .then(res => res.json())
                .then(data => {
                    let cpu = data.cpu_percent.toFixed(0) + ' %';
                    let temp = data.cpu_temp > 0 ? data.cpu_temp.toFixed(0) + ' ˚С' : '-- ˚С';
                    document.getElementById('cpu-line').innerText = 'CPU:   ' + cpu + '    ' + temp;

                    let ramPerc = data.ram_percent.toFixed(0) + ' %';
                    let ramTotal = data.ram_total.toFixed(0) + 'ГБ';
                    document.getElementById('ram-line').innerText = 'RAM:   ' + ramPerc + '    ' + ramTotal;

                    let gpuPerc = data.gpu_percent > 0 ? data.gpu_percent.toFixed(0) + ' %' : '-- %';
                    let gpuTemp = data.gpu_temp > 0 ? data.gpu_temp.toFixed(0) + ' ˚С' : '-- ˚С';
                    document.getElementById('gpu-line').innerText = 'GPU:   ' + gpuPerc + '    ' + gpuTemp;

                    let down = data.net_down > 1024 ? (data.net_down / 1024).toFixed(1) + ' Мб/с' : data.net_down.toFixed(0) + ' Кб/с';
                    let up = data.net_up > 1024 ? (data.net_up / 1024).toFixed(1) + ' Мб/с' : data.net_up.toFixed(0) + ' Кб/с';
                    document.getElementById('net-line').innerText = 'NET:   ↓ ' + down + '   ↑ ' + up;
                })
                .catch(err => console.error(err));
        }
        setInterval(updateStats, 1000);
        updateStats();
    </script>
</body>
</html>`
	w.Header().Set("Content-Type", "application/json; charset=utf-8") // исправлено на html в оригинале, но тут оставим для w.Write
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(html))
}

func handleStats(w http.ResponseWriter, r *http.Request) {
	trackClient(r)

	// CPU Load
	cpuPercents, err := cpu.Percent(0, false)
	var cpuVal float64
	if err == nil && len(cpuPercents) > 0 {
		cpuVal = cpuPercents[0]
	}

	// CPU Temperature
	var tempVal float64
	queryTemp := r.URL.Query().Get("temp")
	if queryTemp != "" {
		if t, err := strconv.ParseFloat(queryTemp, 64); err == nil && t > 0 {
			tempVal = t
		}
	}

	if tempVal == 0 && runtime.GOOS == "windows" {
		cpuTempScript := `
		$t = 0
		try {
			$mmf = [System.IO.MemoryMappedFiles.MemoryMappedFile]::OpenExisting("CoreTempSharedMem")
			$stream = $mmf.CreateViewStream()
			$reader = New-Object System.IO.BinaryReader($stream)
			$bytes = $reader.ReadBytes(2048)
			$reader.Close()
			$stream.Close()
			$mmf.Dispose()
			for ($i = 0; $i -lt ($bytes.Length - 4); $i += 4) {
				$val = [System.BitConverter]::ToSingle($bytes, $i)
				if ($val -gt 15.0 -and $val -lt 110.0) { $t = $val; break }
			}
		} catch {}

		if (-not $t) {
			try {
				$s = Get-WmiObject -Namespace 'root\OpenHardwareMonitor' -Class Sensor -ErrorAction SilentlyContinue | Where-Object {$_.SensorType -eq 'Temperature' -and ($_.Name -match 'CPU' -or $_.Name -match 'Core') -and $_.Name -notmatch 'GPU'} | Select-Object -First 1
				if ($s) { $t = $s.Value }
			} catch {}
		}
		if (-not $t) {
			try {
				$s = Get-WmiObject -Namespace 'root\LibreHardwareMonitor' -Class Sensor -ErrorAction SilentlyContinue | Where-Object {$_.SensorType -eq 'Temperature' -and ($_.Name -match 'CPU' -or $_.Name -match 'Core') -and $_.Name -notmatch 'GPU'} | Select-Object -First 1
				if ($s) { $t = $s.Value }
			} catch {}
		}
		if (-not $t) {
			try {
				$zones = Get-CimInstance -Namespace 'root/wmi' -ClassName MSAcpi_ThermalZoneTemperature -ErrorAction SilentlyContinue
				foreach ($zone in $zones) {
					if ($zone.CurrentTemperature) {
						$val = ($zone.CurrentTemperature / 10.0) - 273.15
						if ($val -gt 15 -and $val -lt 120) { $t = $val; break }
					}
				}
			} catch {}
		}
		if ($t -gt 0) { [Math]::Round($t, 1) }
		`
		cmd := exec.Command("powershell", "-NoProfile", "-Command", cpuTempScript)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		out, err := cmd.Output()
		if err == nil {
			valStr := strings.TrimSpace(string(out))
			if parsed, err := strconv.ParseFloat(valStr, 64); err == nil && parsed > 0 && parsed < 120 {
				tempVal = parsed
			}
		}
	}

	// RAM
	var ramPercent float64
	var ramTotalGB float64
	v, err := mem.VirtualMemory()
	if err == nil {
		ramPercent = v.UsedPercent
		ramTotalGB = float64(v.Total) / (1024 * 1024 * 1024)
	}

	// GPU
	// GPU (NVIDIA + AMD поддержка)
	var gpuTemp float64
	var gpuPerc float64

	if runtime.GOOS == "windows" {
		gpuScript := `
		$gt = 0
		$gl = 0

		# 1. NVIDIA (nvidia-smi)
		try {
			$nsmi = & "nvidia-smi" --query-gpu=temperature.gpu --format=csv,noheader 2>$null
			if ($nsmi) { $gt = [double]($nsmi.Trim().Split("` + "`n" + `")[0]) }
		} catch {}

		# 2. AMD / Общие через Open/Libre Hardware Monitor
		if (-not $gt) {
			try {
				$sensor = Get-WmiObject -Namespace 'root\OpenHardwareMonitor' -Class Sensor -ErrorAction SilentlyContinue | Where-Object {$_.SensorType -eq 'Temperature' -and $_.Name -match 'GPU'} | Select-Object -First 1
				if ($sensor) { $gt = $sensor.Value }
			} catch {}
		}
		if (-not $gt) {
			try {
				$sensor = Get-WmiObject -Namespace 'root\LibreHardwareMonitor' -Class Sensor -ErrorAction SilentlyContinue | Where-Object {$_.SensorType -eq 'Temperature' -and $_.Name -match 'GPU'} | Select-Object -First 1
				if ($sensor) { $gt = $sensor.Value }
			} catch {}
		}

	    # 3. Дополнительный поиск для AMD через WMI (Win32_VideoController или специфичные пространства)
		if (-not $gt) {
			try {
				$amdSensors = Get-CimInstance -Namespace 'root\WMI' -ClassName MSAcpi_ThermalZoneTemperature -ErrorAction SilentlyContinue
				# Если стандартные методы не отдали, пробуем общие счетчики
			} catch {}
		}

		# Загрузка GPU (универсальные счетчики Windows для NVIDIA и AMD)
		try {
			$counters = Get-Counter -Counter "\GPU Engine(*)\Utilization Percentage" -ErrorAction SilentlyContinue
			if ($counters) {
				$total = 0
				foreach ($sample in $counters.CounterSamples) {
					if ($sample.CookedValue -gt 0) { $total += $sample.CookedValue }
				}
				if ($total -gt 100) { $total = 100 }
				if ($total -gt 0) { $gl = $total }
			}
		} catch {}

		[PSCustomObject]@{ Temp = $gt; Load = $gl } | ConvertTo-Json -Compress
		`
		cmd := exec.Command("powershell", "-NoProfile", "-Command", gpuScript)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		out, err := cmd.Output()
		if err == nil {
			var res struct {
				Temp float64 `json:"Temp"`
				Load float64 `json:"Load"`
			}
			if json.Unmarshal(out, &res) == nil {
				if res.Temp > 0 {
					gpuTemp = res.Temp
				}
				if res.Load > 0 {
					gpuPerc = res.Load
				}
			}
		}
	}

	// Net Speed
	ioCounters, err := gopsnet.IOCounters(false)
	if err == nil && len(ioCounters) > 0 {
		now := time.Now()
		totalRecv := ioCounters[0].BytesRecv
		totalSent := ioCounters[0].BytesSent

		if !lastNetCheckTime.IsZero() {
			duration := now.Sub(lastNetCheckTime).Seconds()
			if duration > 0 {
				diffRecv := float64(totalRecv - lastNetBytesRecv)
				diffSent := float64(totalSent - lastNetBytesSent)
				netSpeedDown = (diffRecv / duration) / 1024
				netSpeedUp = (diffSent / duration) / 1024
			}
		}
		lastNetBytesRecv = totalRecv
		lastNetBytesSent = totalSent
		lastNetCheckTime = now
	}

	stats := PCStats{
		CPUPercent: cpuVal,
		CPUTemp:    tempVal,
		RAMPercent: ramPercent,
		RAMTotal:   ramTotalGB,
		GPUPercent: gpuPerc,
		GPUTemp:    gpuTemp,
		NetDown:    netSpeedDown,
		NetUp:      netSpeedUp,
		Port:       currentPort,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

type PCStats struct {
	CPUPercent float64 `json:"cpu_percent"`
	CPUTemp    float64 `json:"cpu_temp"`
	RAMPercent float64 `json:"ram_percent"`
	RAMTotal   float64 `json:"ram_total"`
	GPUPercent float64 `json:"gpu_percent"`
	GPUTemp    float64 `json:"gpu_temp"`
	NetDown    float64 `json:"net_down"`
	NetUp      float64 `json:"net_up"`
	Port       string  `json:"port"`
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
	systray.AddSeparator()
	mExit := systray.AddMenuItem("Exit", "Выход из программы")

	go func() {
		for {
			<-mPort.ClickedCh

			clientsMu.Lock()
			now := time.Now()
			var clientLines []string
			for ip, c := range activeClients {
				if now.Sub(c.LastSeen) < 30*time.Second {
					if c.Name != "" && c.Name != ip {
						clientLines = append(clientLines, fmt.Sprintf("• %s (%s)", c.Name, ip))
					} else {
						clientLines = append(clientLines, fmt.Sprintf("• %s", ip))
					}
				}
			}
			clientsMu.Unlock()

			devicesStr := "Нет активных подключений"
			if len(clientLines) > 0 {
				devicesStr = strings.Join(clientLines, "\n")
			}

			msgText := fmt.Sprintf("Текущий порт: %s\n\nПодключенные устройства:\n%s\n\nДля смены порта введите новое значение:", currentPort, devicesStr)

			psScript := `
			Add-Type -AssemblyName Microsoft.VisualBasic
			$port = [Microsoft.VisualBasic.Interaction]::InputBox('` + msgText + `', 'PCparamWEB — Управление', '` + currentPort + `')
			if ($port -match '^\d+$') { [System.IO.File]::WriteAllText("$env:TEMP\pc_port.tmp", $port) }
			`
			cmd := exec.Command("powershell", "-NoProfile", "-WindowStyle", "Hidden", "-Command", psScript)
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			cmd.Run()

			tempFile := filepath.Join(os.TempDir(), "pc_port.tmp")
			if data, err := os.ReadFile(tempFile); err == nil {
				newPort := strings.TrimSpace(string(data))
				os.Remove(tempFile)
				if newPort != "" && newPort != currentPort {
					savePortConfig(newPort)
					systray.Quit()
					exec.Command(os.Args[0], newPort).Start()
					os.Exit(0)
				}
			}
		}
	}()

	go func() {
		<-mExit.ClickedCh
		systray.Quit()
		os.Exit(0)
	}()
}

func onExit() {}
