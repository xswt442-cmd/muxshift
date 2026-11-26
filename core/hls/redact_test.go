package hls

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestReadFailureDoesNotRetainSignedAddress(t *testing.T) {
	original := fmt.Errorf("连接 SOCKS5://user:proxysecret@[::1]:1080 failed when reading https://example.test/seg.ts?token=secret: %w", context.Canceled)
	err := readFailure("could not read media", original)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation cause was lost: %v", err)
	}
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		if strings.Contains(cause.Error(), "secret") {
			t.Fatal("the error chain retained a credential")
		}
	}
}

// A signed address names one stream for one reader; what identifies the stream
// is scheme, host and path, and what explains a failure is the reason.
func TestStripAddressKeepsTheNameAndDropsTheSecret(t *testing.T) {
	const signed = "https://cdn.example.com/live/index.m3u8?token=TOP_SECRET_123&expires=9#key"
	if got, want := StripAddress(signed), "https://cdn.example.com/live/index.m3u8"; got != want {
		t.Fatalf("StripAddress = %q, want %q", got, want)
	}
	withUser := "https://user:hunter2@cdn.example.com/a/b.m3u8?x=1"
	if got, want := StripAddress(withUser), "https://cdn.example.com/a/b.m3u8"; got != want {
		t.Fatalf("user info: got %q, want %q", got, want)
	}
	if got := StripAddress(""); got != "" {
		t.Fatalf("empty address = %q", got)
	}
}

// An HTTP client quotes the address it could not reach, so an error has to be
// redacted whole rather than only at the address the caller knew about.
func TestRedactRewritesEveryAddressInTheText(t *testing.T) {
	text := `fetch "https://cdn.example.com/seg0.ts?token=TOP_SECRET_123" and https://cdn.example.com/a.m3u8#k failed: 403`
	got := Redact(text)
	if strings.Contains(got, "TOP_SECRET_123") || strings.Contains(got, "#k") || strings.Contains(got, "?token=") {
		t.Fatalf("a secret survived: %q", got)
	}
	for _, keep := range []string{"403", "cdn.example.com/seg0.ts", "a.m3u8"} {
		if !strings.Contains(got, keep) {
			t.Fatalf("redacted text lost %q: %q", keep, got)
		}
	}
}
