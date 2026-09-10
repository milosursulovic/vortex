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

	var closeOnce sync.Once
	closeBoth := func() {
		closeOnce.Do(func() {
			clientConn.Close()
			backendConn.Close()
		})
	}

	done := make(chan struct{}, 2)
	go func() {
		pump(backendConn, clientConn, timeouts.Idle.Duration(), conn.AddReceived)
		done <- struct{}{}
	}()
	go func() {
		pump(clientConn, backendConn, timeouts.Idle.Duration(), conn.AddSent)
		done <- struct{}{}
	}()

	<-done
	conn.SetState(connection.StateClosing)
	closeBoth()
	<-done

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

// pump copies from src to dst, resetting src's read deadline before every
// read when idle > 0, and reports bytes moved via count.
func pump(dst, src net.Conn, idle time.Duration, count func(int)) {
	buf := make([]byte, 32*1024)
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
