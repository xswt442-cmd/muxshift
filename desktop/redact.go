package main

import "github.com/xswt442-cmd/muxshift/core/hls"

// Addresses reach the window from outside, and some of them carry a credential
// in the query string or in the user info: a token that signed one request, a
// session key, a fragment naming a key. The window shows such addresses in a
// status line, writes them to the log, and puts the reason a read failed into an
// error; none of those places may repeat the secret. What stays is the part that
// tells two sources apart — scheme, host, path — and the reason itself, such as
// the HTTP status. The rule itself lives beside the reader that fetches the
// stream, so a place that stores a reason uses the same one.

func stripSecrets(address string) string { return hls.StripAddress(address) }

func redactSecrets(text string) string { return hls.Redact(text) }
