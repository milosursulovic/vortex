// Package ratelimit token-bucket-limits HTTP requests, globally and
// optionally per client IP.
package ratelimit

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/milosursulovic/vortex/internal/config"
)

// staleAfter is how long a per-IP bucket may sit unused before Run reaps
// it, so a stream of distinct client IPs can't grow this map forever.
const staleAfter = 10 * time.Minute

const sweepInterval = 5 * time.Minute

// Limiter enforces a global request rate and, if configured, a separate
// rate per client IP.
type Limiter struct {
	global *rate.Limiter // nil if global limiting is off

	perIPEnabled bool
	perIPRate    rate.Limit
	perIPBurst   int

	mu    sync.Mutex
	perIP map[string]*ipBucket
}

type ipBucket struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// New builds a Limiter from cfg. Call Run in a goroutine to reap idle
// per-IP buckets over time.
func New(cfg config.RateLimitConfig) *Limiter {
	l := &Limiter{perIPEnabled: cfg.PerIP}

	if cfg.RequestsPerSecond > 0 {
		l.global = rate.NewLimiter(rate.Limit(cfg.RequestsPerSecond), cfg.Burst)
	}
	if cfg.PerIP {
		l.perIPRate = rate.Limit(cfg.PerIPRequestsPerSecond)
		l.perIPBurst = cfg.PerIPBurst
		l.perIP = make(map[string]*ipBucket)
	}

	return l
}

// Allow reports whether a request from clientIP may proceed.
func (l *Limiter) Allow(clientIP string) bool {
	if l.global != nil && !l.global.Allow() {
		return false
	}
	if l.perIPEnabled && !l.bucketFor(clientIP).Allow() {
		return false
	}
	return true
}

func (l *Limiter) bucketFor(ip string) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.perIP[ip]
	if !ok {
		b = &ipBucket{limiter: rate.NewLimiter(l.perIPRate, l.perIPBurst)}
		l.perIP[ip] = b
	}
	b.lastSeen = time.Now()
	return b.limiter
}

// Run periodically reaps per-IP buckets that haven't been used recently,
// until ctx is done. It's a no-op if per-IP limiting isn't enabled.
func (l *Limiter) Run(ctx context.Context) {
	if !l.perIPEnabled {
		return
	}

	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.sweep()
		}
	}
}

func (l *Limiter) sweep() {
	cutoff := time.Now().Add(-staleAfter)

	l.mu.Lock()
	defer l.mu.Unlock()
	for ip, b := range l.perIP {
		if b.lastSeen.Before(cutoff) {
			delete(l.perIP, ip)
		}
	}
}
