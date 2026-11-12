package main

import (
	"strings"
)

// Addresses reach the window from outside, and some of them carry a credential
// in the query string or in the user info: a token that signed one request, a
// session key, a fragment naming a key. The window shows such addresses in a
// status line, writes them to the log, and puts the reason a read failed into an
// error; none of those places may repeat the secret. What stays is the part that
// tells two sources apart — scheme, host, path — and the reason itself, such as
// the HTTP status.

// stripSecrets removes the parts of one address a reader must not see: the user
// info before the host, the query, and the fragment.
func stripSecrets(address string) string {
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

// redactSecrets removes the same parts from every http(s) address inside a piece
// of text, so an error written by the HTTP client — which quotes the address it
// could not reach — can be shown and logged whole.
func redactSecrets(text string) string {
	var out strings.Builder
	rest := text
	for {
		start := indexAddress(rest)
		if start < 0 {
			break
		}
		end := start
		for end < len(rest) && !strings.ContainsRune(" \t\r\n\"'<>()[]", rune(rest[end])) {
			end++
		}
		out.WriteString(rest[:start])
		out.WriteString(stripSecrets(rest[start:end]))
		rest = rest[end:]
	}
	out.WriteString(rest)
	return out.String()
}

// indexAddress finds the next http or https address in text, or -1.
func indexAddress(text string) int {
	best := -1
	for _, prefix := range []string{"http://", "https://"} {
		if at := strings.Index(text, prefix); at >= 0 && (best < 0 || at < best) {
			best = at
		}
	}
	return best
}
