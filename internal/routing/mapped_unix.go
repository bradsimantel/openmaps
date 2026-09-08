//go:build darwin || linux

package routing

import (
	"os"
	"syscall"
)

func mapQuery(f *os.File, size int) ([]byte, error) {
	return syscall.Mmap(int(f.Fd()), 0, size, syscall.PROT_READ, syscall.MAP_SHARED)
}
func unmapQuery(b []byte) error { return syscall.Munmap(b) }
