// Package health actively probes backends and drives their UP/DOWN state
// based on consecutive failure/success thresholds, to avoid flapping.
package health

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// Checker probes a single backend address and reports whether it's healthy.
type Checker interface {
	Check(ctx context.Context, address string, timeout time.Duration) error
}

// TCPChecker considers a backend healthy if a TCP connection succeeds.
type TCPChecker struct{}

func (TCPChecker) Check(ctx context.Context, address string, timeout time.Duration) error {
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	return conn.Close()
}

// HTTPChecker considers a backend healthy if GET path returns a 2xx status.
type HTTPChecker struct {
	Path   string
	Client *http.Client
}

func NewHTTPChecker(path string) *HTTPChecker {
	if path == "" {
		path = "/health"
	}
	return &HTTPChecker{Path: path, Client: &http.Client{}}
}

func (h *HTTPChecker) Check(ctx context.Context, address string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+h.Path, nil)
	if err != nil {
		return err
	}

	resp, err := h.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unhealthy status %d", resp.StatusCode)
	}
	return nil
}
