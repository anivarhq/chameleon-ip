//go:build !windows

package core

import "syscall"

// SO_REUSEADDR alone is enough for multicast on these platforms; SO_REUSEPORT
// is not defined on every one of them (Android included), so it is not used.
func setReuseAddr(fd uintptr) error {
	return syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
}
