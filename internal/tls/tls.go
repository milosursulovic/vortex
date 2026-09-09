// Package tls builds a *crypto/tls.Config for a listener's TLS termination
// from VORTEX's config, including optional per-hostname (SNI) certificates.
package tls

import (
	stdtls "crypto/tls"
	"fmt"

	"github.com/milosursulovic/vortex/internal/config"
)

// LoadConfig loads cfg.Certificate/Key as the default certificate and, if
// cfg.SNI is set, additional certificates selected by TLS ClientHello
// server name, falling back to the default when no SNI entry matches.
func LoadConfig(cfg config.TLSConfig) (*stdtls.Config, error) {
	defaultCert, err := stdtls.LoadX509KeyPair(cfg.Certificate, cfg.Key)
	if err != nil {
		return nil, fmt.Errorf("load certificate: %w", err)
	}

	if len(cfg.SNI) == 0 {
		return &stdtls.Config{Certificates: []stdtls.Certificate{defaultCert}}, nil
	}

	byHost := make(map[string]*stdtls.Certificate, len(cfg.SNI))
	for _, entry := range cfg.SNI {
		cert, err := stdtls.LoadX509KeyPair(entry.Certificate, entry.Key)
		if err != nil {
			return nil, fmt.Errorf("load sni certificate for %q: %w", entry.Host, err)
		}
		byHost[entry.Host] = &cert
	}

	return &stdtls.Config{
		Certificates: []stdtls.Certificate{defaultCert},
		GetCertificate: func(hello *stdtls.ClientHelloInfo) (*stdtls.Certificate, error) {
			if cert, ok := byHost[hello.ServerName]; ok {
				return cert, nil
			}
			return &defaultCert, nil
		},
	}, nil
}
