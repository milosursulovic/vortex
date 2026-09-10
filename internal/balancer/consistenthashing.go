package balancer

import (
	"context"
	"fmt"
	"hash/fnv"
	"sort"
	"sync"

	"github.com/milosursulovic/vortex/internal/backend"
	"github.com/milosursulovic/vortex/internal/common"
)

const consistentHashReplicas = 100

type ringEntry struct {
	hash    uint32
	backend *backend.Backend
}

// ConsistentHashing maps clients onto a hash ring of backends so that
// adding or removing a backend only reshuffles a small fraction of clients,
// unlike plain IP hashing (which reshuffles everyone on any pool change).
//
// Building the ring is O(n log n) in the healthy-backend count (n*100
// replicas, sorted); benchmarking showed rebuilding it on every single
// Next() call costs ~600µs and 400KB of allocation at 100 backends versus
// ~300ns for every other algorithm — about 2000x slower. The ring only
// actually needs rebuilding when the healthy set changes, which is rare
// compared to how often Next() is called, so it's cached here and
// invalidated by comparing a cheap fingerprint of the current healthy set.
type ConsistentHashing struct {
	mu          sync.Mutex
	ring        []ringEntry
	fingerprint uint64
}

func NewConsistentHashing() *ConsistentHashing { return &ConsistentHashing{} }

func (*ConsistentHashing) Name() string { return "consistent_hashing" }

func (c *ConsistentHashing) Next(ctx context.Context, pool *backend.Pool) (*backend.Backend, error) {
	healthy := pool.Healthy()
	if len(healthy) == 0 {
		return nil, ErrNoHealthyBackends
	}

	key, ok := common.ClientAddrFromContext(ctx)
	if !ok || key == "" {
		return healthy[0], nil
	}

	ring := c.ringFor(healthy)

	target := hashString(clientHost(key))
	idx := sort.Search(len(ring), func(i int) bool { return ring[i].hash >= target })
	if idx == len(ring) {
		idx = 0
	}

	return ring[idx].backend, nil
}

// ringFor returns the hash ring for the current healthy set, rebuilding it
// only when that set has actually changed since the last call.
func (c *ConsistentHashing) ringFor(healthy []*backend.Backend) []ringEntry {
	fp := fingerprint(healthy)

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.ring == nil || c.fingerprint != fp {
		c.ring = buildRing(healthy)
		c.fingerprint = fp
	}
	return c.ring
}

func buildRing(healthy []*backend.Backend) []ringEntry {
	ring := make([]ringEntry, 0, len(healthy)*consistentHashReplicas)
	for _, b := range healthy {
		for r := 0; r < consistentHashReplicas; r++ {
			ring = append(ring, ringEntry{
				hash:    hashString(fmt.Sprintf("%s#%d", b.Name, r)),
				backend: b,
			})
		}
	}
	sort.Slice(ring, func(i, j int) bool { return ring[i].hash < ring[j].hash })
	return ring
}

// fingerprint cheaply summarizes which backends are currently healthy, in
// their stable pool order, so ringFor can detect "nothing changed" without
// paying the full ring-rebuild cost to find out.
func fingerprint(healthy []*backend.Backend) uint64 {
	h := fnv.New64a()
	for _, b := range healthy {
		_, _ = h.Write([]byte(b.Name))
		_, _ = h.Write([]byte{0})
	}
	return h.Sum64()
}
