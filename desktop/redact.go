package main

import (
	"fmt"

	"github.com/xswt442-cmd/muxshift/core/hls"
)

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

// safeError keeps an error's chain — errors.Is still answers about the error it
// wraps — while the text the window shows and the log prints has its addresses
// stripped. A player or a probe quotes the address it could not open, and a
// signed one carries its token in the query string.
type safeError struct {
	msg string
	err error
}

func (s safeError) Error() string { return s.msg }

func (s safeError) Unwrap() error { return s.err }

// safeOpenError is what every failure from opening a source passes through
// before it is stored for the window: the open error on an item, the session's
// last error, and the outcome a command reports.
func safeOpenError(err error) error {
	if err == nil {
		return nil
	}
	redacted := hls.Redact(err.Error())
	if redacted == err.Error() {
		return err
	}
	return safeError{msg: redacted, err: err}
}

// safeUnwrap keeps a wrapped error's message safe without dropping the reason
// that is not an address, for the few sites that build their own context.
func safeWrap(format string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", format, safeOpenError(err))
}
