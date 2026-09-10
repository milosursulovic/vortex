package netutil

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestListenPlain(t *testing.T) {
	ln, err := Listen(context.Background(), "127.0.0.1:0", Config{})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	if _, err := net.Dial("tcp", ln.Addr().String()); err != nil {
		t.Fatalf("dial: %v", err)
	}
}

func TestListenAppliesBufferAndNoDelay(t *testing.T) {
	ln, err := Listen(context.Background(), "127.0.0.1:0", Config{
		ReadBufferBytes:  8192,
		WriteBufferBytes: 8192,
		NoDelaySet:       true,
		TCPNoDelay:       false, // explicitly re-enable Nagle, to prove the override path runs
	})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	done := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		done <- nil
	}()

	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("accept: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for accept")
	}
}

func TestReusePortGroupSharesOneAddress(t *testing.T) {
	// Grab a free ephemeral port (then release it) so every member of the
	// group below binds the exact same address. All of them set
	// SO_REUSEPORT (NewReusePortGroup forces it), which is what actually
	// lets a port be shared — without it, even a second reuse-port-enabled
	// socket can't join a port whose first occupant never opted in.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	addr := probe.Addr().String()
	probe.Close()

	group, err := NewReusePortGroup(context.Background(), addr, 3, Config{})
	if err != nil {
		t.Fatalf("NewReusePortGroup: %v", err)
	}
	defer func() {
		for _, ln := range group {
			ln.Close()
		}
	}()

	if len(group) != 3 {
		t.Fatalf("expected 3 listeners, got %d", len(group))
	}
	for i, ln := range group {
		if ln.Addr().String() != addr {
			t.Fatalf("listener %d bound to %s, want %s", i, ln.Addr().String(), addr)
		}
	}
}

func TestReusePortGroupCleansUpOnPartialFailure(t *testing.T) {
	// A plain (non-reuseport) listener already holds this address, so a
	// reuse-port group targeting it must fail outright (the kernel only
	// allows sharing a port among sockets that all set SO_REUSEPORT).
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer blocker.Close()

	_, err = NewReusePortGroup(context.Background(), blocker.Addr().String(), 2, Config{})
	if err == nil {
		t.Fatal("expected an error binding a reuse-port group over a non-reuseport listener")
	}
}
