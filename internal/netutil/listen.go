// Package netutil builds tuned TCP listeners. The Go runtime already uses
// epoll internally (via its netpoller) for every network operation on
// Linux, and *net.TCPConn already defaults to TCP_NODELAY on — the "start
// with what Go already does" baseline the spec asks for. This package adds
// only the small set of additional Linux socket knobs that are opt-in and
// safe: SO_REUSEPORT (multiple sockets sharing one port, so the kernel
// spreads accepts across them instead of one accept loop bottlenecking),
// keepalive tuning, and socket buffer sizing. None of it is enabled unless
// configured — Go's defaults are good starting points, not a problem to
// route around.
package netutil

import (
	"context"
	"fmt"
	"net"
	"time"
)

// Config controls socket-level tuning for a listener. Zero values mean
// "use Go's own default" for every field.
type Config struct {
	ReusePort        bool
	KeepAliveEnabled bool
	KeepAlive        time.Duration
	TCPNoDelay       bool // only consulted when NoDelaySet is true
	NoDelaySet       bool
	ReadBufferBytes  int
	WriteBufferBytes int
}

// Listen builds a net.Listener for address with cfg applied. When
// cfg.ReusePort is set, the SO_REUSEPORT socket option is applied before
// bind (via controlReusePort, platform-specific), so multiple listeners
// can share the same address — see NewReusePortGroup for the multi-socket
// case this exists for.
func Listen(ctx context.Context, address string, cfg Config) (net.Listener, error) {
	lc := net.ListenConfig{
		KeepAliveConfig: net.KeepAliveConfig{
			Enable: cfg.KeepAliveEnabled,
			Idle:   cfg.KeepAlive,
		},
	}
	if cfg.ReusePort {
		lc.Control = controlReusePort
	}

	ln, err := lc.Listen(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}

	if cfg.ReadBufferBytes > 0 || cfg.WriteBufferBytes > 0 || cfg.NoDelaySet {
		ln = &tunedListener{Listener: ln, cfg: cfg}
	}
	return ln, nil
}

// NewReusePortGroup binds n independent listening sockets to the same
// address, each with SO_REUSEPORT, so the kernel spreads incoming accepts
// across them instead of one accept loop handling every connection. Each
// returned listener should get its own accept-loop goroutine.
func NewReusePortGroup(ctx context.Context, address string, n int, cfg Config) ([]net.Listener, error) {
	cfg.ReusePort = true

	listeners := make([]net.Listener, 0, n)
	for i := 0; i < n; i++ {
		ln, err := Listen(ctx, address, cfg)
		if err != nil {
			for _, l := range listeners {
				_ = l.Close()
			}
			return nil, fmt.Errorf("reuse_port listener %d/%d: %w", i+1, n, err)
		}
		listeners = append(listeners, ln)
	}
	return listeners, nil
}

// tunedListener applies per-connection socket options that Go only
// exposes on *net.TCPConn (not at listen time): buffer sizes and an
// explicit TCP_NODELAY override.
type tunedListener struct {
	net.Listener
	cfg Config
}

func (l *tunedListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	if tc, ok := conn.(*net.TCPConn); ok {
		if l.cfg.NoDelaySet {
			_ = tc.SetNoDelay(l.cfg.TCPNoDelay)
		}
		if l.cfg.ReadBufferBytes > 0 {
			_ = tc.SetReadBuffer(l.cfg.ReadBufferBytes)
		}
		if l.cfg.WriteBufferBytes > 0 {
			_ = tc.SetWriteBuffer(l.cfg.WriteBufferBytes)
		}
	}
	return conn, nil
}
