package circuitbreaker

import (
	"testing"
	"time"
)

func TestDisabledAlwaysAllows(t *testing.T) {
	b := New(false, 1, time.Hour)
	b.RecordFailure()
	b.RecordFailure()
	if !b.Allow() {
		t.Fatal("disabled breaker must always allow")
	}
}

func TestOpensAfterThreshold(t *testing.T) {
	b := New(true, 3, time.Hour)

	for i := 0; i < 2; i++ {
		b.RecordFailure()
		if !b.Allow() {
			t.Fatalf("should still be allowed before threshold (failure %d)", i+1)
		}
	}
	b.RecordFailure()
	if b.Allow() {
		t.Fatal("expected breaker to be OPEN after threshold failures")
	}
	if b.State() != StateOpen {
		t.Fatalf("expected OPEN, got %s", b.State())
	}
}

func TestSuccessResetsFailureCount(t *testing.T) {
	b := New(true, 3, time.Hour)
	b.RecordFailure()
	b.RecordFailure()
	b.RecordSuccess()
	b.RecordFailure()
	b.RecordFailure()
	if !b.Allow() {
		t.Fatal("failure count should have reset after a success, so 2 more failures must not trip it")
	}
}

func TestHalfOpenAfterTimeoutAllowsOneTrial(t *testing.T) {
	b := New(true, 1, 10*time.Millisecond)
	b.RecordFailure() // -> OPEN
	if b.Allow() {
		t.Fatal("should be OPEN immediately after tripping")
	}

	time.Sleep(20 * time.Millisecond)
	if !b.Allow() {
		t.Fatal("expected HALF_OPEN trial to be allowed after openTimeout elapses")
	}
	if b.State() != StateHalfOpen {
		t.Fatalf("expected HALF_OPEN, got %s", b.State())
	}
	if b.Allow() {
		t.Fatal("a second concurrent trial must not be allowed while HALF_OPEN")
	}
}

func TestHalfOpenSuccessCloses(t *testing.T) {
	b := New(true, 1, 10*time.Millisecond)
	b.RecordFailure()
	time.Sleep(20 * time.Millisecond)
	b.Allow() // transitions to HALF_OPEN
	b.RecordSuccess()
	if b.State() != StateClosed {
		t.Fatalf("expected CLOSED after a successful trial, got %s", b.State())
	}
}

func TestHalfOpenFailureReopens(t *testing.T) {
	b := New(true, 1, 10*time.Millisecond)
	b.RecordFailure()
	time.Sleep(20 * time.Millisecond)
	b.Allow() // transitions to HALF_OPEN
	b.RecordFailure()
	if b.State() != StateOpen {
		t.Fatalf("expected OPEN after a failed trial, got %s", b.State())
	}
}
