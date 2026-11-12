package hls

import "strings"

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

// Redact removes the same parts from every http(s) address inside a piece of
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
		for end < len(rest) && !strings.ContainsRune(" \t\r\n\"'<>()[]", rune(rest[end])) {
			end++
		}
		out.WriteString(rest[:start])
		out.WriteString(StripAddress(rest[start:end]))
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
