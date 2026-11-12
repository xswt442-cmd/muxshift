package main

import (
	"fmt"
	"runtime"
)

// The release script fills these in with -ldflags "-X main.buildVersion=…
// -X main.buildCommit=…". A plain `go build` keeps the defaults below, which
// say what they are instead of passing for a release: a build from a working
// tree has no version and no commit to name.
var (
	buildVersion = "0.0.0-dev"
	buildCommit  = "unknown"
)

// versionLine is what -version prints and what the diagnostics page would show
// without taking the window away from the person asking.
func versionLine() string {
	return fmt.Sprintf("MuxShift %s (%s)", buildVersion, buildCommit)
}

// buildLine adds the toolchain the binary was made with. It is kept apart from
// versionLine because only one of the two belongs in a title or a file name.
func buildLine() string {
	return fmt.Sprintf("%s built with %s %s/%s", versionLine(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
}
