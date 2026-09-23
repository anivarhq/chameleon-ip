package core

import "syscall"

// Windows runs its own WS-Discovery service on 3702, so the port has to be
// shared rather than owned.
func setReuseAddr(fd uintptr) error {
	return syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
}
