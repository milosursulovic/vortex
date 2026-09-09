// Package circuitbreaker implements a per-backend circuit breaker so
// VORTEX stops sending traffic to a backend that's repeatedly failing to
// connect, instead of hammering it and piling up failed attempts.
package circuitbreaker

import (
	"sync"
	"time"
)

// State is the breaker's position in its CLOSED -> OPEN -> HALF_OPEN cycle.
type State int32

const (
	StateClosed State = iota
	StateOpen
	StateHalfOpen
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "CLOSED"
	case StateOpen:
		return "OPEN"
	case StateHalfOpen:
		return "HALF_OPEN"
	default:
		return "UNKNOWN"
	}
}

// Breaker tracks consecutive failures for one backend and decides whether
// new attempts should be allowed through.
type Breaker struct {
	enabled          bool
	failureThreshold int
	openTimeout      time.Duration

	mu       sync.Mutex
	state    State
	failures int
	openedAt time.Time
}

// New builds a Breaker. A disabled breaker always allows requests through.
func New(enabled bool, failureThreshold int, openTimeout time.Duration) *Breaker {
	return &Breaker{
		enabled:          enabled,
		failureThreshold: failureThreshold,
		openTimeout:      openTimeout,
	}
}

// Allow reports whether a new attempt may proceed. While OPEN it refuses
// until openTimeout has elapsed, then transitions to HALF_OPEN and allows
// exactly one trial attempt through.
func (b *Breaker) Allow() bool {
	if !b.enabled {
		return true
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		return true
	case StateHalfOpen:
		return false // a trial attempt is already in flight
	case StateOpen:
		if time.Since(b.openedAt) >= b.openTimeout {
			b.state = StateHalfOpen
			return true
		}
		return false
	default:
		return true
	}
}

// RecordSuccess reports a successful attempt: it closes the breaker,
// resetting the failure count.
func (b *Breaker) RecordSuccess() {
	if !b.enabled {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	b.state = StateClosed
	b.failures = 0
}

// RecordFailure reports a failed attempt. In CLOSED, it opens the breaker
// once failureThreshold consecutive failures accumulate. In HALF_OPEN, the
// failed trial reopens the breaker immediately.
func (b *Breaker) RecordFailure() {
	if !b.enabled {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateHalfOpen:
		b.open()
	case StateClosed:
		b.failures++
		if b.failures >= b.failureThreshold {
			b.open()
		}
	}
}

func (b *Breaker) open() {
	b.state = StateOpen
	b.failures = 0
	b.openedAt = time.Now()
}

// State returns the breaker's current state.
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}
