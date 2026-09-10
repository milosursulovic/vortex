// Package proxy implements the bidirectional TCP data-plane between an
// accepted client connection and a chosen backend.
package proxy

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/milosursulovic/vortex/internal/backend"
	"github.com/milosursulovic/vortex/internal/common"
	"github.com/milosursulovic/vortex/internal/config"
	"github.com/milosursulovic/vortex/internal/connection"
	"github.com/milosursulovic/vortex/internal/metrics"
)

// BackendPicker selects the next backend for a new connection.
type BackendPicker interface {
	Next(ctx context.Context) (*backend.Backend, error)
}

// ServeTCP proxies clientConn to a backend chosen by picker until either
// side closes, then cleans up both connections. It never returns while
// leaving a connection open.
func ServeTCP(clientConn net.Conn, picker BackendPicker, timeouts config.TimeoutsConfig, limits config.LimitsConfig, rec *metrics.Recorder, logger *slog.Logger) {
	defer clientConn.Close()

	conn := connection.New(clientConn.RemoteAddr().String())
	logger.Info("connection_accepted", "connection_id", conn.ID, "client", conn.ClientAddr)

	rec.ConnectionsTotal.Add(1)
	rec.ConnectionsActive.Add(1)
	defer rec.ConnectionsActive.Add(-1)

	ctx := common.WithClientAddr(context.Background(), conn.ClientAddr)
	target, err := selectAvailableBackend(ctx, picker, limits.MaxConnectionsPerBackend)
	if err != nil {
		rec.ErrorsTotal.Add(1)
		logger.Error("backend_selection_failed", "connection_id", conn.ID, "error", err.Error())
		return
	}
	conn.BackendAddr = target.Address
	conn.SetState(connection.StateConnecting)
	target.IncTotalConnections()

	dialer := net.Dialer{Timeout: timeouts.Connect.Duration()}
	dialStart := time.Now()
	backendConn, err := dialer.Dial("tcp", target.Address)
	rec.ObserveBackendLatency(target.Name, time.Since(dialStart))
	if err != nil {
		target.IncFailures()
		target.Breaker().RecordFailure()
		rec.ErrorsTotal.Add(1)
		logger.Error("backend_connect_failed", "connection_id", conn.ID, "backend", target.Name, "address", target.Address, "error", err.Error())
		return
	}
	defer backendConn.Close()
	target.Breaker().RecordSuccess()

	target.IncActiveConnections()
	defer target.DecActiveConnections()

	conn.SetState(connection.StateEstablished)
	logger.Info("backend_selected", "connection_id", conn.ID, "backend", target.Name, "address", target.Address)

	// Half-close, don't hard-close, when one direction finishes: a client
	// that shuts down its write side (done sending) may still be reading a
	// response the backend hasn't finished sending. Closing both
	// connections as soon as either direction returns would truncate that
	// in-flight data. Only fully close once both directions are done.
	done := make(chan struct{}, 2)
	go func() {
		pump(backendConn, clientConn, timeouts.Idle.Duration(), conn.AddReceived)
		halfCloseWrite(backendConn)
		done <- struct{}{}
	}()
	go func() {
		pump(clientConn, backendConn, timeouts.Idle.Duration(), conn.AddSent)
		halfCloseWrite(clientConn)
		done <- struct{}{}
	}()

	<-done
	<-done
	conn.SetState(connection.StateClosing)
	clientConn.Close()
	backendConn.Close()

	conn.SetState(connection.StateClosed)
	rec.BytesReceived.Add(conn.BytesReceived())
	rec.BytesSent.Add(conn.BytesSent())
	logger.Info("connection_closed",
		"connection_id", conn.ID,
		"duration_ms", conn.Duration().Milliseconds(),
		"bytes_received", conn.BytesReceived(),
		"bytes_sent", conn.BytesSent(),
	)
}

// halfCloseWrite signals "no more data coming" to the peer without
// tearing down the whole connection, so the other pump direction (still
// possibly relaying an in-flight response) isn't cut short. A no-op for
// connection types that don't support half-close (e.g. net.Pipe); the idle
// timeout still bounds how long such a peer can be waited on.
func halfCloseWrite(conn net.Conn) {
	type writeCloser interface{ CloseWrite() error }
	if wc, ok := conn.(writeCloser); ok {
		_ = wc.CloseWrite()
	}
}

const pumpBufferSize = 32 * 1024

// pumpBufferPool reuses the 32KB per-direction copy buffer across
// connections instead of allocating (and garbage-collecting) a fresh one
// for every connection-direction. Benchmarked: a plain make() costs ~3.7µs
// and one 32KB allocation per use; pooling cuts that to a few ns and ~0
// allocations. At any real connection churn rate that's 64KB of garbage
// avoided per connection (two directions), so it's adopted here — see
// internal/proxy/buffer_bench_test.go for the benchmark this is based on.
var pumpBufferPool = sync.Pool{
	New: func() any {
		buf := make([]byte, pumpBufferSize)
		return &buf
	},
}

// pump copies from src to dst, resetting src's read deadline before every
// read when idle > 0, and reports bytes moved via count.
func pump(dst, src net.Conn, idle time.Duration, count func(int)) {
	bufPtr := pumpBufferPool.Get().(*[]byte)
	defer pumpBufferPool.Put(bufPtr)
	buf := *bufPtr

	for {
		if idle > 0 {
			_ = src.SetReadDeadline(time.Now().Add(idle))
		}
		n, err := src.Read(buf)
		if n > 0 {
			count(n)
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}
