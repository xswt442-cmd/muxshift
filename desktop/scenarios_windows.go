//go:build windows

package main

// The checks below drive a real Windows player and cannot be built elsewhere,
// so they join the registry here instead of the shared table in scenarios.go.
func init() {
	scenarios["seekboundary"] = runSeekBoundary
	scenarios["seekrefuse"] = runSeekRefuse
	scenarios["seekpaused"] = runSeekPaused
	scenarios["retryprobe"] = runRetryProbe
}
