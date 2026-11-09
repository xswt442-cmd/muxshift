// Command muxshift-host is the native messaging host the browser extension
// talks to. It carries control messages only: the extension names one address
// the user chose, this process checks that the caller is the registered
// extension, and forwards that address to the local MuxShift web service with
// the token the service wrote for it.
//
// The browser starts this process with the caller's origin as the first
// argument and speaks the native messaging framing: a 32-bit little-endian
// length followed by one UTF-8 JSON message. Nothing but that framing may be
// written to stdout; diagnostics go to stderr.
package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// callerOrigin is the only extension allowed to drive this host. It matches the
// keyed extension ID in the extension manifest and allowed_origins in the host
// manifest, so an unpacked build from another path cannot impersonate it.
const callerOrigin = "chrome-extension://agacmebmmcbkclbblmekgdkfcehehccl/"

// maxMessage bounds one request from the extension; the browser never sends
// more than this for a control message.
const maxMessage = 1 << 20

// bridgeFile mirrors the file the web service leaves behind: where it listens
// and which token it expects.
type bridgeFile struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

// request is what the extension asks for.
type request struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

// response is what the extension receives. Reason is written for the user, so a
// refused address is explained instead of being reported as success.
type response struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
	Source *struct {
		ID  string `json:"id"`
		URI string `json:"uri"`
	} `json:"source,omitempty"`
}

func main() {
	if len(os.Args) < 2 {
		fail("no caller origin was given")
	}
	if os.Args[1] != callerOrigin {
		fail(fmt.Sprintf("caller %q is not the registered extension", os.Args[1]))
	}
	var message request
	if err := readMessage(os.Stdin, &message); err != nil {
		fail("cannot read the request: " + err.Error())
	}
	switch message.Type {
	case "ping":
		writeMessage(os.Stdout, response{OK: true})
	case "import":
		writeMessage(os.Stdout, importSource(message.URL))
	default:
		writeMessage(os.Stdout, response{Reason: fmt.Sprintf("unknown request type %q", message.Type)})
	}
}

// importSource forwards one address to the running service. The token never
// leaves this machine and is never written to stdout or the log.
func importSource(address string) response {
	bridge, err := loadBridge()
	if err != nil {
		return response{Reason: err.Error()}
	}
	body, err := json.Marshal(map[string]string{"url": address})
	if err != nil {
		return response{Reason: err.Error()}
	}
	request, err := http.NewRequest(http.MethodPost, bridge.URL+"/api/import", strings.NewReader(string(body)))
	if err != nil {
		return response{Reason: err.Error()}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-MuxShift-Token", bridge.Token)
	client := &http.Client{Timeout: 15 * time.Second}
	reply, err := client.Do(request)
	if err != nil {
		return response{Reason: "the local service is not answering: " + err.Error()}
	}
	defer reply.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(reply.Body, 64<<10))
	if reply.StatusCode != http.StatusOK {
		return response{Reason: fmt.Sprintf("the local service refused the address (%s): %s", reply.Status, strings.TrimSpace(string(raw)))}
	}
	var added struct {
		ID  string `json:"id"`
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(raw, &added); err != nil {
		return response{Reason: "the local service answered something unexpected"}
	}
	result := response{OK: true}
	result.Source = &struct {
		ID  string `json:"id"`
		URI string `json:"uri"`
	}{ID: added.ID, URI: added.URI}
	return result
}

// loadBridge reads the file the web service wrote. MUXSHIFT_BRIDGE_FILE points
// somewhere else when the service was started with a different -token-file.
func loadBridge() (bridgeFile, error) {
	path := os.Getenv("MUXSHIFT_BRIDGE_FILE")
	if path == "" {
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			return bridgeFile{}, errors.New("LOCALAPPDATA is not set, so the bridge file cannot be found")
		}
		path = base + `\MuxShift\bridge.json`
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return bridgeFile{}, fmt.Errorf("the MuxShift web service has not published its address (%s)", path)
	}
	var bridge bridgeFile
	if err := json.Unmarshal(raw, &bridge); err != nil {
		return bridgeFile{}, fmt.Errorf("the bridge file %s is not readable JSON", path)
	}
	if bridge.URL == "" || bridge.Token == "" {
		return bridgeFile{}, fmt.Errorf("the bridge file %s is missing its address or token", path)
	}
	return bridge, nil
}

// readMessage reads one length-prefixed JSON message.
func readMessage(r io.Reader, target any) error {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}
	size := binary.LittleEndian.Uint32(header[:])
	if size == 0 || size > maxMessage {
		return fmt.Errorf("message length %d is out of range", size)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return err
	}
	return json.Unmarshal(payload, target)
}

// writeMessage writes one length-prefixed JSON message.
func writeMessage(w io.Writer, value any) {
	payload, err := json.Marshal(value)
	if err != nil {
		fail("cannot encode the response: " + err.Error())
	}
	var header [4]byte
	binary.LittleEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := w.Write(header[:]); err != nil {
		fail("cannot write the response header: " + err.Error())
	}
	if _, err := w.Write(payload); err != nil {
		fail("cannot write the response: " + err.Error())
	}
}

// fail reports a problem on stderr and exits; stdout must stay protocol-only.
func fail(reason string) {
	fmt.Fprintln(os.Stderr, "muxshift-host: "+reason)
	os.Exit(1)
}
