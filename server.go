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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
	gopsnet "github.com/shirou/gopsutil/v3/net"
)

var (
	lastNetBytesRecv uint64
	lastNetBytesSent uint64
	lastNetCheckTime time.Time
	netSpeedDown     float64
	netSpeedUp       float64

	cachedCPUTemp float64
	lastTempCheck time.Time
	tempCheckMu   sync.Mutex
)

func StartWebServer() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleIndex)
	mux.HandleFunc("/api/stats", handleStats)
	mux.HandleFunc("/api/config", handleApiConfig)

	for {
		addr := ":" + CurrentPort
		log.Printf("Starting server on http://localhost%s", addr)
		err := http.ListenAndServe(addr, mux)
		if err != nil {
			log.Printf("Port %s busy or error: %v. Retrying...", CurrentPort, err)
			time.Sleep(2 * time.Second)
		}
	}
}

func handleApiConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		CfgMu.Lock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(AppCfg)
		CfgMu.Unlock()
	} else if r.Method == http.MethodPost {
		var newCfg AppConfig
		if err := json.NewDecoder(r.Body).Decode(&newCfg); err == nil {
			CfgMu.Lock()
			AppCfg.ShowCPULoad = newCfg.ShowCPULoad
			AppCfg.ShowCPUTemp = newCfg.ShowCPUTemp
			AppCfg.ShowRAMGraph = newCfg.ShowRAMGraph
			AppCfg.ShowGPULoad = newCfg.ShowGPULoad
			AppCfg.ShowGPUTemp = newCfg.ShowGPUTemp
			AppCfg.ShowNETGraph = newCfg.ShowNETGraph
			if newCfg.Theme == "dark" || newCfg.Theme == "light" {
				AppCfg.Theme = newCfg.Theme
			}
			CfgMu.Unlock()
			SaveConfig()
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusBadRequest)
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
	ClientsMu.Lock()
	defer ClientsMu.Unlock()

	client, exists := ActiveClients[ip]
	client.IP = ip
	client.LastSeen = time.Now()
	if customName != "" {
		client.Name = customName
	}

	if !exists {
		if customName == "" {
			client.Name = ""
		}
		ActiveClients[ip] = client

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
						ClientsMu.Lock()
						if c, ok := ActiveClients[targetIP]; ok {
							c.Name = resolved
							ActiveClients[targetIP] = c
						}
						ClientsMu.Unlock()
					}
				}
			}(ip)
		}
	} else {
		ActiveClients[ip] = client
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
        :root {
            --bg-color: #000000;
            --text-color: #aaaaaa;
            --header-color: #777777;
            --border-color: #222222;
            --canvas-bg: rgba(255, 255, 255, 0.02);
            --temp-color: #d4883a;
        }
        body.light {
            --bg-color: #f5f5f5;
            --text-color: #222222;
            --header-color: #555555;
            --border-color: #dddddd;
            --canvas-bg: rgba(0, 0, 0, 0.03);
            --temp-color: #b86214;
        }
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
            background: var(--bg-color);
            color: var(--text-color);
            margin: 0;
            padding: 4px 8px;
            display: flex;
            flex-direction: column;
            align-items: flex-start;
            justify-content: flex-start;
            height: 100vh;
            box-sizing: border-box;
            transition: background 0.3s, color 0.3s;
        }
        .header-container {
            width: 100%;
            display: flex;
            justify-content: space-between;
            align-items: center;
            margin-bottom: 2px;
        }
        h1 {
            font-size: 0.75rem;
            color: var(--header-color);
            margin: 0;
            font-weight: normal;
        }
        .nav-buttons {
            display: flex;
            gap: 4px;
        }
        .config-btn {
            background: none;
            border: 1px solid var(--border-color);
            color: var(--text-color);
            font-size: 0.65rem;
            padding: 1px 6px;
            cursor: pointer;
            border-radius: 3px;
        }
        .metric-block {
            width: 100%;
            margin-bottom: 3px;
        }
        .stats-line {
            font-size: 1.65rem;
            color: var(--text-color);
            font-weight: bold;
            white-space: pre;
            text-align: left;
            width: 100%;
            margin-top: 2px;
        }
        .net-line {
            font-size: 1.32rem; /* уменьшено на 20% от 1.65rem */
        }
        .val-temp {
            color: var(--temp-color);
        }
        canvas {
            width: 100% !important;
            height: 32px !important;
            background: var(--canvas-bg);
            border-radius: 2px;
            display: block;
            margin-top: 1px;
        }
        #config-modal {
            display: none;
            position: fixed;
            top: 0; left: 0; width: 100%; height: 100%;
            background: rgba(0,0,0,0.6);
            justify-content: center;
            align-items: center;
            z-index: 1000;
        }
        .modal-content {
            background: var(--bg-color);
            border: 1px solid var(--border-color);
            padding: 15px;
            border-radius: 6px;
            width: 250px;
            box-shadow: 0 4px 12px rgba(0,0,0,0.5);
        }
        .modal-content h3 {
            margin-top: 0;
            font-size: 0.9rem;
            color: var(--text-color);
        }
        .modal-content label {
            display: block;
            font-size: 0.75rem;
            margin-bottom: 6px;
            cursor: pointer;
        }
        .modal-content select {
            width: 100%;
            background: var(--bg-color);
            color: var(--text-color);
            border: 1px solid var(--border-color);
            padding: 3px;
            margin-bottom: 10px;
            border-radius: 3px;
        }
        .modal-buttons {
            display: flex;
            justify-content: flex-end;
            gap: 6px;
            margin-top: 10px;
        }
        .modal-buttons button {
            background: none;
            border: 1px solid var(--border-color);
            color: var(--text-color);
            padding: 3px 8px;
            cursor: pointer;
            border-radius: 3px;
            font-size: 0.75rem;
        }
    </style>
</head>
<body id="body">
    <div class="header-container">
        <h1>PCparamWEB</h1>
        <div class="nav-buttons">
            <button class="config-btn" onclick="openConfigModal()">Config</button>
        </div>
    </div>

    <!-- CPU -->
    <div class="metric-block">
        <div class="stats-line" id="cpu-line">CPU:   -- %    -- ˚С</div>
        <canvas id="cpu-load-canvas"></canvas>
        <canvas id="cpu-temp-canvas"></canvas>
    </div>

    <!-- RAM -->
    <div class="metric-block">
        <div class="stats-line" id="ram-line">RAM:   -- %    -- ГБ</div>
        <canvas id="ram-canvas"></canvas>
    </div>

    <!-- GPU -->
    <div class="metric-block">
        <div class="stats-line" id="gpu-line">GPU:   -- %    -- ˚С</div>
        <canvas id="gpu-load-canvas"></canvas>
        <canvas id="gpu-temp-canvas"></canvas>
    </div>

    <!-- NET -->
    <div class="metric-block">
        <div class="stats-line net-line" id="net-line">NET:   ↓ --      ↑ --</div>
        <canvas id="net-canvas"></canvas>
    </div>

    <!-- Модальное окно настроек -->
    <div id="config-modal">
        <div class="modal-content">
            <h3>Config</h3>
            <label>Theme:
                <select id="cfg-theme">
                    <option value="dark">dark</option>
                    <option value="light">light</option>
                </select>
            </label>
            <label><input type="checkbox" id="cfg-cpu-load"> CPU Load Graph</label>
            <label><input type="checkbox" id="cfg-cpu-temp"> CPU Temp Graph</label>
            <label><input type="checkbox" id="cfg-ram"> RAM Graph</label>
            <label><input type="checkbox" id="cfg-gpu-load"> GPU Load Graph</label>
            <label><input type="checkbox" id="cfg-gpu-temp"> GPU Temp Graph</label>
            <label><input type="checkbox" id="cfg-net"> NET Graph</label>
            <div class="modal-buttons">
                <button onclick="closeConfigModal()">Cancel</button>
                <button onclick="saveConfigModal()">Save</button>
            </div>
        </div>
    </div>

    <script>
        let historyData = { 
            cpuLoad: [], cpuTemp: [], 
            ram: [], 
            gpuLoad: [], gpuTemp: [], 
            netDown: [], netUp: [] 
        };
        const maxHistory = 40;
        let counter = 0; // Для замедления движения графиков в 3 раза

        function applyTheme(theme) {
            const body = document.getElementById('body');
            if (theme === 'light') {
                body.classList.add('light');
            } else {
                body.classList.remove('light');
            }
        }

        function openConfigModal() {
            fetch('/api/config')
                .then(res => res.json())
                .then(cfg => {
                    document.getElementById('cfg-theme').value = cfg.theme || 'dark';
                    document.getElementById('cfg-cpu-load').checked = cfg.show_cpu_load;
                    document.getElementById('cfg-cpu-temp').checked = cfg.show_cpu_temp;
                    document.getElementById('cfg-ram').checked = cfg.show_ram_graph;
                    document.getElementById('cfg-gpu-load').checked = cfg.show_gpu_load;
                    document.getElementById('cfg-gpu-temp').checked = cfg.show_gpu_temp;
                    document.getElementById('cfg-net').checked = cfg.show_net_graph;
                    document.getElementById('config-modal').style.display = 'flex';
                });
        }

        function closeConfigModal() {
            document.getElementById('config-modal').style.display = 'none';
        }

        function saveConfigModal() {
            const newCfg = {
                port: "",
                theme: document.getElementById('cfg-theme').value,
                show_cpu_load: document.getElementById('cfg-cpu-load').checked,
                show_cpu_temp: document.getElementById('cfg-cpu-temp').checked,
                show_ram_graph: document.getElementById('cfg-ram').checked,
                show_gpu_load: document.getElementById('cfg-gpu-load').checked,
                show_gpu_temp: document.getElementById('cfg-gpu-temp').checked,
                show_net_graph: document.getElementById('cfg-net').checked
            };
            fetch('/api/config', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(newCfg)
            }).then(() => {
                closeConfigModal();
                loadConfigAndApply();
            });
        }

        function drawGraph(canvasId, data, color, maxVal = 100) {
            const canvas = document.getElementById(canvasId);
            if (!canvas) return;
            const ctx = canvas.getContext('2d');
            const w = canvas.width = canvas.offsetWidth;
            const h = canvas.height = canvas.offsetHeight;

            ctx.clearRect(0, 0, w, h);
            if (data.length < 2) return;

            ctx.beginPath();
            ctx.strokeStyle = color;
            ctx.lineWidth = 1.5;
            const step = w / (maxHistory - 1);
            let dynamicMax = maxVal;
            for (let i = 0; i < data.length; i++) {
                if (data[i] > dynamicMax) dynamicMax = data[i] * 1.1;
            }

            for (let i = 0; i < data.length; i++) {
                let val = data[i];
                let x = i * step;
                let y = h - (val / dynamicMax) * (h - 4) - 2;
                if (i === 0) ctx.moveTo(x, y);
                else ctx.lineTo(x, y);
            }
            ctx.stroke();
        }

        function drawDualGraph(canvasId, data1, data2, color1, color2) {
            const canvas = document.getElementById(canvasId);
            if (!canvas) return;
            const ctx = canvas.getContext('2d');
            const w = canvas.width = canvas.offsetWidth;
            const h = canvas.height = canvas.offsetHeight;

            ctx.clearRect(0, 0, w, h);
            if (data1.length < 2 && data2.length < 2) return;

            let maxVal = 10;
            for (let i = 0; i < data1.length; i++) {
                if (data1[i] > maxVal) maxVal = data1[i];
                if (data2[i] > maxVal) maxVal = data2[i];
            }
            maxVal *= 1.1;

            function drawLine(arr, color) {
                ctx.beginPath();
                ctx.strokeStyle = color;
                ctx.lineWidth = 1.5;
                const step = w / (maxHistory - 1);
                for (let i = 0; i < arr.length; i++) {
                    let val = arr[i];
                    let x = i * step;
                    let y = h - (val / maxVal) * (h - 4) - 2;
                    if (i === 0) ctx.moveTo(x, y);
                    else ctx.lineTo(x, y);
                }
                ctx.stroke();
            }

            drawLine(data1, color1);
            drawLine(data2, color2);
        }

        function updateStats() {
            fetch('/api/stats')
                .then(res => res.json())
                .then(data => {
                    let cpuP = data.cpu_percent.toFixed(0) + ' %';
                    let cpuT = data.cpu_temp > 0 ? data.cpu_temp.toFixed(0) + ' ˚С' : '-- ˚С';
                    document.getElementById('cpu-line').innerHTML = 'CPU:   ' + cpuP + '    <span class="val-temp">' + cpuT + '</span>';

                    let ramPerc = data.ram_percent.toFixed(0) + ' %';
                    let ramTotal = data.ram_total.toFixed(0) + 'ГБ';
                    document.getElementById('ram-line').innerText = 'RAM:   ' + ramPerc + '    ' + ramTotal;

                    let gpuPerc = data.gpu_percent > 0 ? data.gpu_percent.toFixed(0) + ' %' : '-- %';
                    let gpuTemp = data.gpu_temp > 0 ? data.gpu_temp.toFixed(0) + ' ˚С' : '-- ˚С';
                    document.getElementById('gpu-line').innerHTML = 'GPU:   ' + gpuPerc + '    <span class="val-temp">' + gpuTemp + '</span>';

                    let downStr = data.net_down > 1024 ? (data.net_down / 1024).toFixed(1) + ' Мб/с' : data.net_down.toFixed(0) + ' Кб/с';
                    let upStr = data.net_up > 1024 ? (data.net_up / 1024).toFixed(1) + ' Мб/с' : data.net_up.toFixed(0) + ' Кб/с';
                    document.getElementById('net-line').innerHTML = 'NET:   <span style="color: #0088FF;">↓</span> ' + downStr + '   <span style="color: #4CAF50;">↑</span> ' + upStr;

                    // Замедляем добавление новых точек в истории в 3 раза, чтобы графики двигались медленнее
                    counter++;
                    if (counter % 3 === 0) {
                        historyData.cpuLoad.push(data.cpu_percent);
                        historyData.cpuTemp.push(data.cpu_temp > 0 ? data.cpu_temp : 0);
                        if (historyData.cpuLoad.length > maxHistory) historyData.cpuLoad.shift();
                        if (historyData.cpuTemp.length > maxHistory) historyData.cpuTemp.shift();

                        historyData.ram.push(data.ram_percent);
                        if (historyData.ram.length > maxHistory) historyData.ram.shift();

                        let gLoad = data.gpu_percent > 0 ? data.gpu_percent : 0;
                        let gTemp = data.gpu_temp > 0 ? data.gpu_temp : 0;
                        historyData.gpuLoad.push(gLoad);
                        historyData.gpuTemp.push(gTemp);
                        if (historyData.gpuLoad.length > maxHistory) historyData.gpuLoad.shift();
                        if (historyData.gpuTemp.length > maxHistory) historyData.gpuTemp.shift();

                        historyData.netDown.push(data.net_down);
                        historyData.netUp.push(data.net_up);
                        if (historyData.netDown.length > maxHistory) historyData.netDown.shift();
                        if (historyData.netUp.length > maxHistory) historyData.netUp.shift();
                    }

                    // Перерисовка каждый кадр/секунду плавная
                    drawGraph('cpu-load-canvas', historyData.cpuLoad, '#4CAF50', 100);
                    drawGraph('cpu-temp-canvas', historyData.cpuTemp, '#d4883a', 100);
                    drawGraph('ram-canvas', historyData.ram, '#FF9800', 100);
                    drawGraph('gpu-load-canvas', historyData.gpuLoad, '#E91E63', 100);
                    drawGraph('gpu-temp-canvas', historyData.gpuTemp, '#d4883a', 100);
                    drawDualGraph('net-canvas', historyData.netDown, historyData.netUp, '#0088FF', '#4CAF50');
                })
                .catch(err => console.error(err));
        }

        function loadConfigAndApply() {
            fetch('/api/config')
                .then(res => res.json())
                .then(cfg => {
                    applyTheme(cfg.theme);
                    document.getElementById('cpu-load-canvas').style.display = cfg.show_cpu_load ? 'block' : 'none';
                    document.getElementById('cpu-temp-canvas').style.display = cfg.show_cpu_temp ? 'block' : 'none';
                    document.getElementById('ram-canvas').style.display = cfg.show_ram_graph ? 'block' : 'none';
                    document.getElementById('gpu-load-canvas').style.display = cfg.show_gpu_load ? 'block' : 'none';
                    document.getElementById('gpu-temp-canvas').style.display = cfg.show_gpu_temp ? 'block' : 'none';
                    document.getElementById('net-canvas').style.display = cfg.show_net_graph ? 'block' : 'none';
                });
        }

        loadConfigAndApply();
        setInterval(updateStats, 1000);
    </script>
</body>
</html>`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(html))
}

func handleStats(w http.ResponseWriter, r *http.Request) {
	trackClient(r)

	cpuPercents, err := cpu.Percent(0, false)
	var cpuVal float64
	if err == nil && len(cpuPercents) > 0 {
		cpuVal = cpuPercents[0]
	}

	tempCheckMu.Lock()
	if time.Since(lastTempCheck) > 1*time.Second {
		if runtime.GOOS == "windows" {
			exePath, err := os.Executable()
			if err == nil {
				helperPath := filepath.Join(filepath.Dir(exePath), "temp_helper.exe")
				if _, err := os.Stat(helperPath); err == nil {
					cmd := exec.Command(helperPath)
					cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
					out, err := cmd.Output()
					if err == nil {
						var helperRes struct {
							CPUTemp float64 `json:"cpu_temp"`
						}
						if json.Unmarshal(out, &helperRes) == nil && helperRes.CPUTemp > 0 {
							cachedCPUTemp = helperRes.CPUTemp
						}
					}
				}
			}

			if cachedCPUTemp == 0 {
				wmiScript := `
				$t = 0
				try {
					$sensor = Get-WmiObject -Namespace 'root\OpenHardwareMonitor' -Class Sensor -ErrorAction SilentlyContinue | Where-Object {$_.SensorType -eq 'Temperature' -and ($_.Name -match 'CPU' -or $_.Name -match 'Core')} | Select-Object -First 1
					if ($sensor) { $t = $sensor.Value }
				} catch {}
				if (-not $t) {
					try {
						$sensor = Get-CimInstance -Namespace 'root\Wmi' -ClassName MSAcpi_ThermalZoneTemperature -ErrorAction SilentlyContinue | Select-Object -First 1
						if ($sensor -and $sensor.CurrentTemperature) { $t = ($sensor.CurrentTemperature / 10) - 273.15 }
					} catch {}
				}
				[PSCustomObject]@{ Temp = $t } | ConvertTo-Json -Compress
				`
				cmdWmi := exec.Command("powershell", "-NoProfile", "-Command", wmiScript)
				cmdWmi.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
				outWmi, errWmi := cmdWmi.Output()
				if errWmi == nil {
					var wmiRes struct {
						Temp float64 `json:"Temp"`
					}
					if json.Unmarshal(outWmi, &wmiRes) == nil && wmiRes.Temp > 0 {
						cachedCPUTemp = wmiRes.Temp
					}
				}
			}
		}
		lastTempCheck = time.Now()
	}
	tempVal := cachedCPUTemp
	tempCheckMu.Unlock()

	var ramPercent float64
	var ramTotalGB float64
	v, err := mem.VirtualMemory()
	if err == nil {
		ramPercent = v.UsedPercent
		ramTotalGB = float64(v.Total) / (1024 * 1024 * 1024)
	}

	var gpuTemp float64
	var gpuPerc float64

	if runtime.GOOS == "windows" {
		gpuScript := `
		$gt = 0
		$gl = 0
		try {
			$nsmi = & "nvidia-smi" --query-gpu=temperature.gpu,utilization.gpu --format=csv,noheader,nounits 2>$null
			if ($nsmi) {
				$parts = $nsmi.Trim().Split("` + "`n" + `")[0].Split(',')
				if ($parts.Count -ge 2) {
					$gt = [double]$parts[0].Trim()
					$gl = [double]$parts[1].Trim()
				}
			}
		} catch {}

		if (-not $gt) {
			try {
				$sensor = Get-WmiObject -Namespace 'root\OpenHardwareMonitor' -Class Sensor -ErrorAction SilentlyContinue | Where-Object {$_.SensorType -eq 'Temperature' -and $_.Name -match 'GPU'} | Select-Object -First 1
				if ($sensor) { $gt = $sensor.Value }
			} catch {}
		}

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
		Port:       CurrentPort,
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
