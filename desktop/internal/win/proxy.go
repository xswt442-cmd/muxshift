package win

// This window reaches the network through Go's HTTP client, which honours
// HTTP_PROXY and HTTPS_PROXY but not the Windows Internet Settings that a
// browser follows. On a machine that needs the system proxy, a local file still
// plays while a live stream does not, and a live stream is the one thing this
// build cannot make up for. applySystemProxy copies the setting into the
// environment before the first request, so the streams a person can watch in
// their browser are the ones this window can open.
//
// The registry read itself lives in systemproxy_windows.go; on any other
// platform there is no such setting, so the build stays portable.

import (
	"os"
	"strings"
)

// ApplyProxy copies the Windows proxy into the environment and returns the
// address it used, with any credentials removed, or "" when nothing was set.
// An environment that already names a proxy is left alone: it is the more
// specific instruction.
func ApplyProxy() string {
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
