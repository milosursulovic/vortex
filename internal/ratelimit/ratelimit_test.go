package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/milosursulovic/vortex/internal/config"
)

func TestGlobalLimitEnforced(t *testing.T) {
	l := New(config.RateLimitConfig{RequestsPerSecond: 1000, Burst: 2})

	if !l.Allow("1.1.1.1") {
		t.Fatal("expected 1st request within burst to be allowed")
	}
	if !l.Allow("1.1.1.1") {
		t.Fatal("expected 2nd request within burst to be allowed")
	}
	if l.Allow("1.1.1.1") {
		t.Fatal("expected 3rd request to exceed burst and be denied")
	}
}

func TestPerIPIsolatesClients(t *testing.T) {
	l := New(config.RateLimitConfig{
		PerIP:                  true,
		PerIPRequestsPerSecond: 1000,
		PerIPBurst:             1,
	})

	if !l.Allow("1.1.1.1") {
		t.Fatal("expected client A's 1st request to be allowed")
	}
	if l.Allow("1.1.1.1") {
		t.Fatal("expected client A's 2nd request to exceed its burst")
	}
	if !l.Allow("2.2.2.2") {
		t.Fatal("client B should have its own independent bucket")
	}
}

func TestNoLimitsConfiguredAllowsEverything(t *testing.T) {
	l := New(config.RateLimitConfig{})
	for i := 0; i < 100; i++ {
		if !l.Allow("1.1.1.1") {
			t.Fatal("no limits configured should never deny")
		}
	}
}

func TestSweepReapsStaleBuckets(t *testing.T) {
	l := New(config.RateLimitConfig{PerIP: true, PerIPRequestsPerSecond: 10, PerIPBurst: 10})
	l.Allow("1.1.1.1")

	l.mu.Lock()
	l.perIP["1.1.1.1"].lastSeen = time.Now().Add(-staleAfter - time.Minute)
	l.mu.Unlock()

	l.sweep()

	l.mu.Lock()
	_, ok := l.perIP["1.1.1.1"]
	l.mu.Unlock()
	if ok {
		t.Fatal("expected stale bucket to be reaped")
	}
}

func TestRunNoopWhenPerIPDisabled(t *testing.T) {
	l := New(config.RateLimitConfig{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	l.Run(ctx) // must return promptly, not block
}
