//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

const (
	internetSettingsKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	hkeyCurrentUser     = 0x80000001
	rrfRTSZ             = 0x00000002
	rrfRTDWORD          = 0x00000010
)

var procRegGetValueW = syscall.NewLazyDLL("advapi32.dll").NewProc("RegGetValueW")

// windowsProxy reads ProxyEnable and ProxyServer from the settings a browser
// follows.
func windowsProxy() (string, bool) {
	enabled, ok := regDWORD("ProxyEnable")
	if !ok || enabled == 0 {
		return "", false
	}
	server, ok := regString("ProxyServer")
	if !ok {
		return "", false
	}
	picked := pickProxyServer(server)
	return picked, picked != ""
}

func regString(name string) (string, bool) {
	subkey, err := syscall.UTF16PtrFromString(internetSettingsKey)
	if err != nil {
		return "", false
	}
	value, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return "", false
	}
	buffer := make([]uint16, 1024)
	size := uint32(len(buffer) * 2)
	result, _, _ := procRegGetValueW.Call(
		uintptr(hkeyCurrentUser),
		uintptr(unsafe.Pointer(subkey)),
		uintptr(unsafe.Pointer(value)),
		uintptr(rrfRTSZ),
		0,
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if result != 0 {
		return "", false
	}
	return syscall.UTF16ToString(buffer), true
}

func regDWORD(name string) (uint32, bool) {
	subkey, err := syscall.UTF16PtrFromString(internetSettingsKey)
	if err != nil {
		return 0, false
	}
	value, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0, false
	}
	var data uint32
	size := uint32(4)
	result, _, _ := procRegGetValueW.Call(
		uintptr(hkeyCurrentUser),
		uintptr(unsafe.Pointer(subkey)),
		uintptr(unsafe.Pointer(value)),
		uintptr(rrfRTDWORD),
		0,
		uintptr(unsafe.Pointer(&data)),
		uintptr(unsafe.Pointer(&size)),
	)
	if result != 0 {
		return 0, false
	}
	return data, true
}
