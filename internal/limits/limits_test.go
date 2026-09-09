package limits

import (
	"log/slog"
	"net"
	"testing"
	"time"
)

func TestConnLimiterUnlimitedWhenZero(t *testing.T) {
	l := NewConnLimiter(0)
	for i := 0; i < 1000; i++ {
		if !l.TryAcquire() {
			t.Fatal("unlimited limiter should always acquire")
		}
	}
}

func TestConnLimiterEnforcesCap(t *testing.T) {
	l := NewConnLimiter(2)
	if !l.TryAcquire() {
		t.Fatal("expected 1st acquire to succeed")
	}
	if !l.TryAcquire() {
		t.Fatal("expected 2nd acquire to succeed")
	}
	if l.TryAcquire() {
		t.Fatal("expected 3rd acquire to fail at cap")
	}

	l.Release()
	if !l.TryAcquire() {
		t.Fatal("expected acquire to succeed after a release")
	}
}

func TestWrapListenerRejectsOverCap(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	limiter := NewConnLimiter(1)
	wrapped := WrapListener(ln, limiter, discardLogger(), "test")

	// First connection: consume the only slot without closing it, so the
	// listener's internal accepted-conn tracking reflects "at capacity".
	done := make(chan net.Conn, 2)
	go func() {
		for i := 0; i < 2; i++ {
			c, err := wrapped.Accept()
			if err != nil {
				close(done)
				return
			}
			done <- c
		}
	}()

	c1, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial 1: %v", err)
	}
	defer c1.Close()

	accepted1 := <-done
	if accepted1 == nil {
		t.Fatal("expected first connection to be accepted")
	}
	defer accepted1.Close()

	// Second dial should be accepted at the TCP level, then immediately
	// closed by the limiter since the cap (1) is already held.
	c2, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial 2: %v", err)
	}
	defer c2.Close()

	buf := make([]byte, 1)
	c2.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = c2.Read(buf)
	if err == nil {
		t.Fatal("expected the over-cap connection to be closed by the server")
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(discardWriter{}, nil))
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
