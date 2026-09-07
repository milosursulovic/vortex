// Package common holds small helpers shared across otherwise-unrelated
// internal packages, to avoid import cycles between them.
package common

import "context"

type clientAddrKeyType struct{}

var clientAddrKey = clientAddrKeyType{}

// WithClientAddr attaches the originating client address to ctx, for
// balancer strategies (IP hash, consistent hashing) that key on it.
func WithClientAddr(ctx context.Context, addr string) context.Context {
	return context.WithValue(ctx, clientAddrKey, addr)
}

// ClientAddrFromContext retrieves an address set by WithClientAddr.
func ClientAddrFromContext(ctx context.Context) (string, bool) {
	addr, ok := ctx.Value(clientAddrKey).(string)
	return addr, ok
}
