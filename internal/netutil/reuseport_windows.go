//go:build windows

package netutil

import (
	"fmt"
	"syscall"
)

// controlReusePort: SO_REUSEPORT has no equivalent on Windows. Config
// validation rejects reuse_port before this would ever be called in
// practice; this exists so the package still builds there.
func controlReusePort(_, _ string, _ syscall.RawConn) error {
	return fmt.Errorf("reuse_port is not supported on windows")
}
