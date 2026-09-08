package main

import (
	"encoding/json"
	"os"
	"syscall"
	"unsafe"
)

type TempData struct {
	CPUTemp float64 `json:"cpu_temp"`
	Debug   string  `json:"debug,omitempty"`
}

func main() {
	var cpuTemp float64 = 0.0
	var debugMsg string

	dllPath := "WinRing0x64.dll"
	if _, err := os.Stat(dllPath); os.IsNotExist(err) {
		outputJSON(0, "WinRing0x64.dll not found in directory")
		return
	}

	winRing0 := syscall.NewLazyDLL(dllPath)

	// Официальные имена функций в OlsApi / WinRing0 DLL
	initProc := winRing0.NewProc("InitializeOls")
	deinitProc := winRing0.NewProc("DeinitializeOls")
	rdmsrProc := winRing0.NewProc("Rdmsr")

	// Инициализация библиотеки и драйвера
	r1, _, _ := initProc.Call()
	if r1 == 0 {
		debugMsg = "InitializeOls returned 0 (Check Administrator privileges)"
		outputJSON(cpuTemp, debugMsg)
		return
	}
	defer deinitProc.Call()

	var eax, edx uint32

	// ==========================================
	// 1. ВЕТКА INTEL: Читаем IA32_THERM_STATUS (0x19C)
	// ==========================================
	r2, _, _ := rdmsrProc.Call(
		uintptr(0x19C),
		uintptr(unsafe.Pointer(&eax)),
		uintptr(unsafe.Pointer(&edx)),
	)

	if r2 != 0 {
		digitalReadout := float64((eax >> 16) & 0x7F)
		// Проверяем, что значение в адек范围е для Intel DTS
		if digitalReadout > 0 && digitalReadout < 120 {
			cpuTemp = 100.0 - digitalReadout
			debugMsg = "Success (Intel)"
			outputJSON(cpuTemp, debugMsg)
			return
		}
	}

	// ==========================================
	// 2. ВЕТКА AMD RYZEN: Читаем Zen MSR (0xC0010064)
	// (Срабатывает только если Intel-регистр вернул нули или ошибку)
	// ==========================================
	rAmd, _, _ := rdmsrProc.Call(
		uintptr(0xC0010064),
		uintptr(unsafe.Pointer(&eax)),
		uintptr(unsafe.Pointer(&edx)),
	)

	if rAmd != 0 {
		// Формула для процессоров AMD Zen (Family 17h/19h): битовое поле температуры
		tempRaw := (eax >> 21) & 0x7FF
		if tempRaw > 0 {
			amdTemp := float64(tempRaw) * 0.125
			if amdTemp > 0 && amdTemp < 125 {
				cpuTemp = amdTemp
				debugMsg = "Success (AMD Ryzen)"
				outputJSON(cpuTemp, debugMsg)
				return
			}
		}
	}

	// Если оба метода не дали результата
	debugMsg = "Rdmsr execution failed or unsupported CPU architecture"
	outputJSON(cpuTemp, debugMsg)
}

func outputJSON(temp float64, dbg string) {
	data := TempData{CPUTemp: temp, Debug: dbg}
	json.NewEncoder(os.Stdout).Encode(data)
}
