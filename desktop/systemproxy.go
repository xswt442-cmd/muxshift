package main

import (
	"os"
	"strings"
	"syscall"
	"unsafe"
)

// This window reaches the network through Go's HTTP client, which honours
// HTTP_PROXY and HTTPS_PROXY but not the Windows Internet Settings that a
// browser follows. On a machine that needs the system proxy, a local file still
// plays while a live stream does not, and a live stream is the one thing this
// build cannot make up for. applySystemProxy copies the setting into the
// environment before the first request, so the streams a person can watch in
// their browser are the ones this window can open.

const (
	internetSettingsKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	hkeyCurrentUser     = 0x80000001
	rrfRTSZ             = 0x00000002
	rrfRTDWORD          = 0x00000010
)

var procRegGetValueW = syscall.NewLazyDLL("advapi32.dll").NewProc("RegGetValueW")

// applySystemProxy copies the Windows proxy into the environment and returns the
// address it used, with any credentials removed, or "" when nothing was set.
// An environment that already names a proxy is left alone: it is the more
// specific instruction.
func applySystemProxy() string {
	if os.Getenv("HTTP_PROXY") != "" || os.Getenv("HTTPS_PROXY") != "" {
		return ""
	}
	server, ok := windowsProxy()
	if !ok {
		return ""
	}
	address := "http://" + server
	os.Setenv("HTTP_PROXY", address)
	os.Setenv("HTTPS_PROXY", address)
	return redactProxy(server)
}

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

// pickProxyServer reads a ProxyServer value, which is either one address for
// every protocol or a semicolon-separated list of protocol=address entries.
func pickProxyServer(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if !strings.Contains(value, "=") {
		return value
	}
	httpAddress := ""
	for _, entry := range strings.Split(value, ";") {
		name, address, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		address = strings.TrimSpace(address)
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "https":
			return address
		case "http":
			if httpAddress == "" {
				httpAddress = address
			}
		}
	}
	return httpAddress
}

// redactProxy drops the credentials of a proxy address so a log line cannot
// carry them.
func redactProxy(server string) string {
	if at := strings.LastIndex(server, "@"); at >= 0 {
		return server[at+1:]
	}
	return server
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
