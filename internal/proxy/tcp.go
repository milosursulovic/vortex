// Package proxy implements the bidirectional TCP data-plane between an
// accepted client connection and a chosen backend.
package proxy

import (
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/milosursulovic/vortex/internal/backend"
	"github.com/milosursulovic/vortex/internal/config"
	"github.com/milosursulovic/vortex/internal/connection"
)

// BackendPicker selects the next backend for a new connection.
type BackendPicker interface {
	Next() (*backend.Backend, error)
}

// ServeTCP proxies clientConn to a backend chosen by picker until either
// side closes, then cleans up both connections. It never returns while
// leaving a connection open.
func ServeTCP(clientConn net.Conn, picker BackendPicker, timeouts config.TimeoutsConfig, logger *slog.Logger) {
	defer clientConn.Close()

	conn := connection.New(clientConn.RemoteAddr().String())
	logger.Info("connection_accepted", "connection_id", conn.ID, "client", conn.ClientAddr)

	target, err := picker.Next()
	if err != nil {
		logger.Error("backend_selection_failed", "connection_id", conn.ID, "error", err.Error())
		return
	}
	conn.BackendAddr = target.Address
	conn.SetState(connection.StateConnecting)
	target.IncTotalConnections()

	dialer := net.Dialer{Timeout: timeouts.Connect.Duration()}
	backendConn, err := dialer.Dial("tcp", target.Address)
	if err != nil {
		target.IncFailures()
		logger.Error("backend_connect_failed", "connection_id", conn.ID, "backend", target.Name, "address", target.Address, "error", err.Error())
		return
	}
	defer backendConn.Close()

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
