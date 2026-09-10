package proxy

import (
	"sync"
	"testing"
)

// These benchmarks isolate the one question spec section 49 asks before
// adopting sync.Pool anywhere: does pooling the 32KB pump buffer actually
// help, measured against the pattern VORTEX uses it in (one buffer
// allocated per connection-direction, then discarded when the connection
// closes)?
//
// sink forces the buffer to actually escape to the heap, the same way it
// does in real pump() by being passed to net.Conn.Read (an interface
// method the compiler can't see through). Without this, escape analysis
// proves the buffer never leaves the loop body and elides the allocation
// entirely, making the benchmark measure nothing.
var sink []byte

func BenchmarkBufferPlainAlloc(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf := make([]byte, 32*1024)
		buf[0] = 1
		sink = buf
	}
}

var benchBufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, 32*1024)
		return &buf
	},
}

func BenchmarkBufferSyncPool(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		bufPtr := benchBufPool.Get().(*[]byte)
		(*bufPtr)[0] = 1
		sink = *bufPtr
		benchBufPool.Put(bufPtr)
	}
}

// The *Concurrent variants mirror real load: many goroutines each
// acquiring/releasing one buffer at once, as the two pump() goroutines per
// connection would across many simultaneous connections.
func BenchmarkBufferPlainAllocConcurrent(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		var local []byte
		for pb.Next() {
			buf := make([]byte, 32*1024)
			buf[0] = 1
			local = buf
		}
		sink = local
	})
}

func BenchmarkBufferSyncPoolConcurrent(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		var local []byte
		for pb.Next() {
			bufPtr := benchBufPool.Get().(*[]byte)
			(*bufPtr)[0] = 1
			local = *bufPtr
			benchBufPool.Put(bufPtr)
		}
		sink = local
	})
}
