package main

import (
	"runtime"
	"strings"
	"testing"
)

// The version line is the only thing a release tells a person who runs
// `muxshift.exe -version`, so it must name the build and the toolchain and not
// depend on a window, a display, or a media address to do it.
func TestTheVersionLineNamesTheBuildAndTheToolchain(t *testing.T) {
	line := buildLine()
	for _, want := range []string{"MuxShift", buildVersion, buildCommit, runtime.Version(), runtime.GOOS} {
		if !strings.Contains(line, want) {
			t.Fatalf("the build line does not name %q: %q", want, line)
		}
	}
	if strings.Contains(versionLine(), runtime.GOOS) {
		t.Fatalf("the short version line carries the platform too: %q", versionLine())
	}
}

// A build that was not stamped must say so rather than look like a release.
func TestAnUnstampedBuildSaysSo(t *testing.T) {
	if buildCommit != "unknown" {
		t.Skip("this binary was built with a commit stamped in")
	}
	if !strings.Contains(buildLine(), "unknown") {
		t.Fatalf("an unstamped build does not say its commit is unknown: %q", buildLine())
	}
}
