package hls

import "errors"

// errorsIs keeps table-driven checks readable when a case names a sentinel.
func errorsIs(err, target error) bool {
	return errors.Is(err, target)
}
