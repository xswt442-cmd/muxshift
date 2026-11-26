package netguard

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublicIP(t *testing.T) {
	for _, address := range []string{"9.9.9.9", "2606:4700::1111", "::ffff:9.9.9.9"} {
		if !PublicIP(netip.MustParseAddr(address)) {
			t.Errorf("ordinary public address %s was refused", address)
		}
	}
	for _, address := range []string{
		"0.0.0.1", "10.0.0.1", "100.64.0.1", "127.0.0.1", "169.254.0.1", "172.16.0.1",
		"192.0.0.1", "192.0.2.1", "192.168.0.1", "198.18.0.1", "198.51.100.1", "203.0.113.1", "240.0.0.1",
		"::1", "::2", "::ffff:127.0.0.1", "64:ff9b::1", "64:ff9b:1::1", "100::1",
		"2001::1", "2001:db8::1", "2002::1", "3fff::1", "fc00::1", "fe80::1", "fec0::1",
	} {
		if PublicIP(netip.MustParseAddr(address)) {
			t.Errorf("special address %s was classified as public", address)
		}
	}
}

func TestClientRequiresExplicitLocalAddress(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, "#EXTM3U")
	}))
	defer server.Close()

	if response, err := Client(time.Second, false).Get(server.URL); !errors.Is(err, ErrDestination) {
		if response != nil {
			response.Body.Close()
		}
		t.Fatalf("public media client returned %v, want a destination refusal", err)
	}
	if requests.Load() != 0 {
		t.Fatal("the denied connection reached the local media server")
	}
	response, err := Client(time.Second, true).Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || requests.Load() != 1 {
		t.Fatalf("explicit local request returned HTTP %d with %d requests", response.StatusCode, requests.Load())
	}
}

func TestValidatePublicAddressesRequiresEveryAddressToBePublic(t *testing.T) {
	public := netip.MustParseAddr("9.9.9.9")
	reserved := netip.MustParseAddr("192.0.2.1")
	if err := validatePublicAddresses([]netip.Addr{public}); err != nil {
		t.Fatalf("public addresses refused: %v", err)
	}
	if err := validatePublicAddresses([]netip.Addr{public, reserved}); !errors.Is(err, ErrDestination) {
		t.Fatalf("mixed addresses returned %v, want destination refusal", err)
	}
}

func TestPinRequestKeepsOriginalHTTPAuthority(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://media.example.test:8443/master.m3u8?profile=low", nil)
	if err != nil {
		t.Fatal(err)
	}
	pinned := pinRequest(req, netip.MustParseAddr("2606:4700::1111"), "8443")
	if pinned.URL.Host != "[2606:4700::1111]:8443" || pinned.Host != "media.example.test:8443" ||
		req.URL.Host != "media.example.test:8443" || pinned.URL.RequestURI() != req.URL.RequestURI() {
		t.Fatal("pinning changed the original HTTP authority or the playlist path")
	}
}
