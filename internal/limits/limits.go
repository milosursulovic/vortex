// Package limits enforces VORTEX-wide resource caps (connection counts) so
// it can't be pushed into unbounded memory growth under load.
package limits

import (
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
)

// ConnLimiter caps the number of concurrently held connections. A max of 0
// or less means unlimited.
type ConnLimiter struct {
	max     int64
	current atomic.Int64
}

func NewConnLimiter(max int) *ConnLimiter {
	return &ConnLimiter{max: int64(max)}
}

// TryAcquire reserves one slot, reporting whether it succeeded.
func (c *ConnLimiter) TryAcquire() bool {
	if c.max <= 0 {
		return true
	}
	for {
		cur := c.current.Load()
		if cur >= c.max {
			return false
		}
		if c.current.CompareAndSwap(cur, cur+1) {
			return true
		}
	}
}

// Release frees one previously acquired slot.
func (c *ConnLimiter) Release() {
	if c.max <= 0 {
		return
	}
	c.current.Add(-1)
}

// Current reports how many slots are currently held.
func (c *ConnLimiter) Current() int64 {
	return c.current.Load()
}

// WrapListener gates ln.Accept() behind limiter: a connection accepted past
// the cap is closed immediately instead of being handed to the caller.
// A nil limiter (or one with no configured max) is a no-op passthrough.
func WrapListener(ln net.Listener, limiter *ConnLimiter, logger *slog.Logger, name string) net.Listener {
	if limiter == nil || limiter.max <= 0 {
		return ln
	}
	return &limitedListener{Listener: ln, limiter: limiter, logger: logger, name: name}
}

type limitedListener struct {
	net.Listener
	limiter *ConnLimiter
	logger  *slog.Logger
	name    string
}

func (l *limitedListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if l.limiter.TryAcquire() {
			return &releasingConn{Conn: conn, limiter: l.limiter}, nil
		}
		l.logger.Warn("connection_rejected", "listener", l.name, "reason", "max_connections exceeded")
		conn.Close()
	}
}

// releasingConn releases its ConnLimiter slot exactly once, on Close.
type releasingConn struct {
	net.Conn
	limiter *ConnLimiter
	once    sync.Once
}

func (c *releasingConn) Close() error {
	c.once.Do(c.limiter.Release)
	return c.Conn.Close()
}
