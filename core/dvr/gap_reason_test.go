package dvr

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xswt442-cmd/muxshift/core/hls"
)

// A gap is shown in a window and printed by every log that describes the
// buffer, so the reason it keeps must not repeat the address that produced it:
// a signed segment carries its token in the query, and the HTTP client quotes
// the address it could not fetch.
func TestAGapReasonDoesNotKeepASignedAddress(t *testing.T) {
	buffer, err := New(Options{Dir: t.TempDir(), Source: "signed", SourceEpoch: 1})
	if err != nil {
		t.Fatalf("new buffer: %v", err)
	}
	refused := errors.New(`fetch segment "http://127.0.0.1:9/seg7.ts?token=TOP_SECRET_123": 403 Forbidden`)
	buffer.noteGap(hls.Segment{Sequence: 7}, refused)

	gaps := buffer.Gaps()
	if len(gaps) != 1 {
		t.Fatalf("gaps = %+v, want one", gaps)
	}
	reason := gaps[0].Reason
	if strings.Contains(reason, "TOP_SECRET_123") || strings.Contains(reason, "?token=") {
		t.Fatalf("the gap reason keeps the secret: %q", reason)
	}
	for _, keep := range []string{"segment 7", "403"} {
		if !strings.Contains(reason, keep) {
			t.Fatalf("the gap reason lost %q: %q", keep, reason)
		}
	}
}

// The same through the path the app uses: a signed segment address is fetched,
// refused, and recorded as a gap.
func TestAFetchedSegmentThatIsRefusedLeavesASafeGap(t *testing.T) {
	var asked string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.String()
		http.Error(w, "no", http.StatusForbidden)
	}))
	t.Cleanup(server.Close)

	buffer, err := New(Options{Dir: t.TempDir(), Source: "signed", SourceEpoch: 1})
	if err != nil {
		t.Fatalf("new buffer: %v", err)
	}
	segment := hls.Segment{
		Sequence: 3,
		URI:      server.URL + "/seg3.ts?token=TOP_SECRET_123",
	}
	if _, err := buffer.Add(context.Background(), server.Client(), segment); err == nil {
		t.Fatal("a refused segment was stored")
	}
	gaps := buffer.Gaps()
	if len(gaps) != 1 {
		t.Fatalf("gaps = %+v, want one", gaps)
	}
	if strings.Contains(gaps[0].Reason, "TOP_SECRET_123") {
		t.Fatalf("the stored reason repeats the token: %q", gaps[0].Reason)
	}
	if !strings.Contains(gaps[0].Reason, "403") {
		t.Fatalf("the stored reason lost the status: %q", gaps[0].Reason)
	}
	if !strings.Contains(asked, "token=TOP_SECRET_123") {
		t.Fatalf("the server was asked for %q, want the signed address", asked)
	}
}
