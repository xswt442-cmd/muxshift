package main

import "testing"

func TestPickProxyServer(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{"one address for everything", "127.0.0.1:9091", "127.0.0.1:9091"},
		{"padded address", "  127.0.0.1:9091  ", "127.0.0.1:9091"},
		{"https entry wins over http", "http=10.0.0.1:3128;https=10.0.0.2:3129", "10.0.0.2:3129"},
		{"http entry used when only it is set", "http=10.0.0.1:3128;socks=10.0.0.3:1080", "10.0.0.1:3128"},
		{"nothing set", "", ""},
		{"spaces only", "   ", ""},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := pickProxyServer(test.value); got != test.want {
				t.Fatalf("pickProxyServer(%q) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}

func TestRedactProxy(t *testing.T) {
	if got := redactProxy("user:secret@127.0.0.1:3128"); got != "127.0.0.1:3128" {
		t.Fatalf("redactProxy kept credentials: %q", got)
	}
	if got := redactProxy("127.0.0.1:9091"); got != "127.0.0.1:9091" {
		t.Fatalf("redactProxy changed an address without credentials: %q", got)
	}
}

func TestApplySystemProxyKeepsAnExistingSetting(t *testing.T) {
	// A proxy named in the environment is the more specific instruction, and
	// the one a person can set without touching Windows.
	t.Setenv("HTTP_PROXY", "http://env.example:3128")
	t.Setenv("HTTPS_PROXY", "")
	if got := applySystemProxy(); got != "" {
		t.Fatalf("applySystemProxy overrode the environment with %q", got)
	}
}
