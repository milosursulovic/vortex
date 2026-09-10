// Package metrics holds VORTEX's process-wide runtime counters, exposed
// via the admin API's /admin/stats endpoint.
package metrics

import "sync/atomic"

// Stats is a set of atomic counters safe for concurrent use from the TCP
// and HTTP data planes.
type Stats struct {
	ConnectionsActive atomic.Int64
	ConnectionsTotal  atomic.Int64
	RequestsTotal     atomic.Int64
	ResponsesTotal    atomic.Int64
	ErrorsTotal       atomic.Int64
	BytesReceived     atomic.Int64
	BytesSent         atomic.Int64
}

// New returns a zeroed Stats.
func New() *Stats {
	return &Stats{}
}

// Snapshot is a point-in-time copy of Stats, suitable for JSON encoding.
type Snapshot struct {
	Connections struct {
		Active int64 `json:"active"`
		Total  int64 `json:"total"`
	} `json:"connections"`
	Requests struct {
		Total     int64 `json:"total"`
		Responses int64 `json:"responses"`
		Errors    int64 `json:"errors"`
	} `json:"requests"`
	Bytes struct {
		Received int64 `json:"received"`
		Sent     int64 `json:"sent"`
	} `json:"bytes"`
}

func (s *Stats) Snapshot() Snapshot {
	var snap Snapshot
	snap.Connections.Active = s.ConnectionsActive.Load()
	snap.Connections.Total = s.ConnectionsTotal.Load()
	snap.Requests.Total = s.RequestsTotal.Load()
	snap.Requests.Responses = s.ResponsesTotal.Load()
	snap.Requests.Errors = s.ErrorsTotal.Load()
	snap.Bytes.Received = s.BytesReceived.Load()
	snap.Bytes.Sent = s.BytesSent.Load()
	return snap
}
