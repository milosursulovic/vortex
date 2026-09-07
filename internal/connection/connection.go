// Package connection tracks the lifecycle of a single proxied connection.
package connection

import (
	"crypto/rand"
	"encoding/hex"
	"sync/atomic"
	"time"
)

// State is a connection's position in its lifecycle.
type State int32

const (
	StateAccepted State = iota
	StateConnecting
	StateEstablished
	StateClosing
	StateClosed
)

func (s State) String() string {
	switch s {
	case StateAccepted:
		return "ACCEPTED"
	case StateConnecting:
		return "CONNECTING"
	case StateEstablished:
		return "ESTABLISHED"
	case StateClosing:
		return "CLOSING"
	case StateClosed:
		return "CLOSED"
	default:
		return "UNKNOWN"
	}
}

// Connection tracks one client<->backend proxied TCP connection.
type Connection struct {
	ID          string
	ClientAddr  string
	BackendAddr string
	CreatedAt   time.Time

	bytesReceived atomic.Int64 // client -> backend
	bytesSent     atomic.Int64 // backend -> client
	state         atomic.Int32
}

// New creates a Connection in StateAccepted with a random ID.
func New(clientAddr string) *Connection {
	c := &Connection{
		ID:         newID(),
		ClientAddr: clientAddr,
		CreatedAt:  time.Now(),
	}
	c.state.Store(int32(StateAccepted))
	return c
}

func (c *Connection) SetState(s State)        { c.state.Store(int32(s)) }
func (c *Connection) State() State            { return State(c.state.Load()) }
func (c *Connection) AddReceived(n int)       { c.bytesReceived.Add(int64(n)) }
func (c *Connection) AddSent(n int)           { c.bytesSent.Add(int64(n)) }
func (c *Connection) BytesReceived() int64    { return c.bytesReceived.Load() }
func (c *Connection) BytesSent() int64        { return c.bytesSent.Load() }
func (c *Connection) Duration() time.Duration { return time.Since(c.CreatedAt) }

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
