package version

import (
	"fmt"
	"runtime"
)

// The release script fills these in with -ldflags "-X
// github.com/xswt442-cmd/muxshift/desktop/internal/version.BuildVersion=…
// -X …BuildCommit=…". A plain `go build` keeps the defaults below, which say
// what they are instead of passing for a release: a build from a working tree
// has no version and no commit to name.
var (
	BuildVersion = "0.0.0-dev"
	BuildCommit  = "unknown"
)

// Line is what -version prints and what the diagnostics page would show without
// taking the window away from the person asking.
func Line() string {
	return fmt.Sprintf("MuxShift %s (%s)", BuildVersion, BuildCommit)
}

// BuildLine adds the toolchain the binary was made with. It is kept apart from
// Line because only one of the two belongs in a title or a file name.
func BuildLine() string {
	return fmt.Sprintf("%s built with %s %s/%s", Line(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
}
