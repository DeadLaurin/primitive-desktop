//go:build !darwin || !cgo

package main

import (
	"errors"
	"image"
)

// platformDecode is a no-op on platforms without a system image framework
// available to cgo builds. The desktop app still decodes every format the Go
// standard library supports.
func platformDecode([]byte) (image.Image, error) {
	return nil, errors.New("no platform image decoder available")
}
