package hls

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/xswt442-cmd/muxshift/core"
)

// A stream can be signed: the address carries a token in its query, a key in its
// fragment, or a credential in its user info. Those parts let one reader fetch
// one stream, and they are not something a window, a log or a stored reason may
// repeat. What identifies a stream is the scheme, the host and the path; what
// explains a failure is the reason, such as an HTTP status.

// StripAddress removes the user info, the query and the fragment from one
// address, leaving the part that names the stream.
func StripAddress(address string) string {
	if address == "" {
		return address
	}
	if cut := strings.IndexAny(address, "?#"); cut >= 0 {
		address = address[:cut]
	}
	if scheme := strings.Index(address, "://"); scheme >= 0 {
		rest := address[scheme+3:]
		if at := strings.LastIndex(rest, "@"); at >= 0 && !strings.Contains(rest[:at], "/") {
			address = address[:scheme+3] + rest[at+1:]
		}
	}
	return address
}

// Redact removes the same parts from every HTTP or SOCKS address inside a piece of
// text, so an error written by an HTTP client — which quotes the address it
// could not reach — can be kept whole without keeping the secret.
func Redact(text string) string {
	var out strings.Builder
	rest := text
	for {
		start := indexAddress(rest)
		if start < 0 {
			break
		}
		end := start
		for end < len(rest) && !strings.ContainsRune(" \t\r\n\"'<>()", rune(rest[end])) {
			end++
		}
		out.WriteString(rest[:start])
		out.WriteString(StripAddress(rest[start:end]))
		rest = rest[end:]
	}
	out.WriteString(rest)
	return out.String()
}

// indexAddress finds the next HTTP or SOCKS address in text, or -1.
func indexAddress(text string) int {
	for i := range len(text) {
		switch text[i] {
		case 'h', 'H', 's', 'S':
			for _, prefix := range []string{"http://", "https://", "socks5://", "socks5h://"} {
				if len(text)-i >= len(prefix) && strings.EqualFold(text[i:i+len(prefix)], prefix) {
					return i
				}
			}
		}
	}
	return -1
}

// readFailure retains known causes without exposing an original transport
// error that can carry a signed URL in its unwrap chain.
func readFailure(message string, err error) error {
	if err == nil {
		return errors.New(message + ": media ended before the probe was ready")
	}
	text := Redact(message + ": " + err.Error())
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, core.ErrInvalid, core.ErrCredentialsInURI} {
		if errors.Is(err, cause) {
			return fmt.Errorf("%s: %w", text, cause)
		}
	}
	return errors.New(text)
}
