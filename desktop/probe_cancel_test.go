package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stallProgram builds a program that outlives the test by a minute, so a test can
// prove the decoder was killed rather than waited for.
func stallProgram(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "stall.go")
	body := "package main\n\nimport \"time\"\n\nfunc main() { time.Sleep(60 * time.Second) }\n"
	if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
		t.Fatalf("write the helper: %v", err)
	}
	bin := filepath.Join(dir, "stall.exe")
	build := exec.Command("go", "build", "-o", bin, src)
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("cannot build the helper: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return bin
}

// TestCancellingADecodeEndsTheChild checks the guarantee the close path depends
// on: cancelling the session's context ends the decoder it started. The helper
// sleeps for a minute, so returning at all is proof the child was killed; the pid
// it reports is the one the test watched.
func TestCancellingADecodeEndsTheChild(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH, so the helper cannot be built")
	}
	bin := stallProgram(t)
	old := decodeProgram
	decodeProgram = bin
	t.Cleanup(func() { decodeProgram = old })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pids := make(chan int, 1)
	done := make(chan error, 1)
	started := time.Now()
	go func() {
		_, err := decodeEnvelope(ctx, "stall://nowhere", 0, time.Second, func(pid int) {
			pids <- pid
		})
		done <- err
	}()

	var pid int
	select {
	case pid = <-pids:
	case err := <-done:
		t.Fatalf("the decoder ended before it was cancelled: %v", err)
	case <-time.After(20 * time.Second):
		t.Fatal("the decoder never reported a pid")
	}
	if pid <= 0 {
		t.Fatalf("the decoder reported pid %d, want a real one", pid)
	}
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a cancelled decode returned no error")
		}
		if !strings.Contains(err.Error(), "stopped") {
			t.Fatalf("the cancelled decode says %q, want it to say it stopped", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("the decoder with pid %d outlived its cancellation", pid)
	}
	if elapsed := time.Since(started); elapsed > 30*time.Second {
		t.Fatalf("the cancelled decode took %s", elapsed)
	}
}

// TestStoppingAProbeDropsItsResult checks the second guarantee: after the session
// closed, a comparison that was in flight cannot become part of its account, even
// if its goroutine reaches the drawing thread later.
func TestStoppingAProbeDropsItsResult(t *testing.T) {
	app := &appState{sync: newSyncState()}
	ctx, cancel := context.WithCancel(context.Background())
	app.probeRunning = true
	app.probeAttempt = 4
	app.probeCtx, app.probeCancel = ctx, cancel
	app.probeChild.Store(4242)

	if !app.stopProbe("the check closed the session") {
		t.Fatal("stopProbe found nothing to stop")
	}
	if app.probeRunning {
		t.Fatal("the probe is still marked as running")
	}
	if app.probeAttempt != 5 {
		t.Fatalf("probeAttempt = %d, want 5: the cancelled attempt must not be reusable", app.probeAttempt)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("the decoder's context is still alive after stopProbe")
	}

	app.applyProbeResult(4, alignSuggestion{CoreOffset: 9 * time.Second}, nil, alignWindow{}, nil, alignWindow{}, nil)
	if app.syncNote != "" {
		t.Fatalf("a cancelled result reached the status bar: %q", app.syncNote)
	}
	if app.probeDone == 4 {
		t.Fatal("a cancelled result was recorded as the newest finished attempt")
	}
	if app.lastProbe.CoreOffset != 0 {
		t.Fatalf("a cancelled result became the account: %+v", app.lastProbe)
	}
}

// TestShutdownCancelsTheComparisonInFlight checks that closing the session is
// what cancels the decoder, not a separate button the user has to find.
func TestShutdownCancelsTheComparisonInFlight(t *testing.T) {
	app := &appState{sync: newSyncState()}
	ctx, cancel := context.WithCancel(context.Background())
	app.probeRunning = true
	app.probeAttempt = 1
	app.probeCtx, app.probeCancel = ctx, cancel
	app.shutdown()

	select {
	case <-ctx.Done():
	default:
		t.Fatal("shutdown left the comparison's context alive")
	}
	if app.probeRunning {
		t.Fatal("shutdown left the probe marked as running")
	}
	if app.probeCancel != nil || app.probeCtx != nil {
		t.Fatal("shutdown kept the cancelled context around")
	}
}
