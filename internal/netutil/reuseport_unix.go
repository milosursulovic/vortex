//go:build !windows

package netutil

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// controlReusePort sets SO_REUSEPORT on the socket before bind, letting
// multiple listeners share one address/port.
func controlReusePort(_, _ string, c syscall.RawConn) error {
	var sockErr error
	err := c.Control(func(fd uintptr) {
		sockErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
	})
	if err != nil {
		return err
	}
	return sockErr
}
