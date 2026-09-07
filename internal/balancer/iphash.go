package balancer

import (
	"context"
	"hash/fnv"
	"net"

	"github.com/milosursulovic/vortex/internal/backend"
	"github.com/milosursulovic/vortex/internal/common"
)

// IPHash deterministically maps a client IP to a healthy backend, so the
// same client keeps landing on the same backend as long as it stays UP.
type IPHash struct{}

func NewIPHash() *IPHash { return &IPHash{} }

func (*IPHash) Name() string { return "ip_hash" }

func (*IPHash) Next(ctx context.Context, pool *backend.Pool) (*backend.Backend, error) {
	healthy := pool.Healthy()
	if len(healthy) == 0 {
		return nil, ErrNoHealthyBackends
	}

	key, ok := common.ClientAddrFromContext(ctx)
	if !ok || key == "" {
		return healthy[0], nil
	}

	idx := hashString(clientHost(key)) % uint32(len(healthy))
	return healthy[idx], nil
}

// clientHost strips the port from a host:port address, if present.
func clientHost(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

func hashString(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}
