//go:build !darwin && !linux

package routing

import (
	"fmt"
	"os"
)

func mapQuery(_ *os.File, _ int) ([]byte, error) {
	return nil, fmt.Errorf("mapped routing is supported on Linux and macOS")
}
func unmapQuery(_ []byte) error { return nil }
