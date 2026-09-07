package balancer

import (
	"context"
	"fmt"
	"sort"

	"github.com/milosursulovic/vortex/internal/backend"
	"github.com/milosursulovic/vortex/internal/common"
)

const consistentHashReplicas = 100

// ConsistentHashing maps clients onto a hash ring of backends so that
// adding or removing a backend only reshuffles a small fraction of clients,
// unlike plain IP hashing (which reshuffles everyone on any pool change).
type ConsistentHashing struct{}

func NewConsistentHashing() *ConsistentHashing { return &ConsistentHashing{} }

func (*ConsistentHashing) Name() string { return "consistent_hashing" }

func (*ConsistentHashing) Next(ctx context.Context, pool *backend.Pool) (*backend.Backend, error) {
	healthy := pool.Healthy()
	if len(healthy) == 0 {
		return nil, ErrNoHealthyBackends
	}

	key, ok := common.ClientAddrFromContext(ctx)
	if !ok || key == "" {
		return healthy[0], nil
	}

	type ringEntry struct {
		hash    uint32
		backend *backend.Backend
	}

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

	target := hashString(clientHost(key))
	idx := sort.Search(len(ring), func(i int) bool { return ring[i].hash >= target })
	if idx == len(ring) {
		idx = 0
	}

	return ring[idx].backend, nil
}
