// Package config loads and validates VORTEX's YAML configuration.
package config

import (
	"fmt"
	"os"
	"runtime"

	"gopkg.in/yaml.v3"
)

// Config is the root VORTEX configuration, matching vortex.yaml.
type Config struct {
	Server         ServerConfig         `yaml:"server" json:"server"`
	Admin          AdminConfig          `yaml:"admin" json:"admin"`
	Logging        LoggingConfig        `yaml:"logging" json:"logging"`
	Listeners      []ListenerConfig     `yaml:"listeners" json:"listeners"`
	Backends       []BackendConfig      `yaml:"backends" json:"backends"` // flat pool used by TCP listeners
	BackendPools   []BackendPoolConfig  `yaml:"backend_pools" json:"backend_pools"`
	Routes         []RouteConfig        `yaml:"routes" json:"routes"`
	LoadBalancing  LoadBalancingConfig  `yaml:"load_balancing" json:"load_balancing"`
	HealthCheck    HealthCheckConfig    `yaml:"health_check" json:"health_check"`
	Timeouts       TimeoutsConfig       `yaml:"timeouts" json:"timeouts"`
	Limits         LimitsConfig         `yaml:"limits" json:"limits"`
	RateLimit      RateLimitConfig      `yaml:"rate_limit" json:"rate_limit"`
	CircuitBreaker CircuitBreakerConfig `yaml:"circuit_breaker" json:"circuit_breaker"`
	Retry          RetryConfig          `yaml:"retry" json:"retry"`
}

type ServerConfig struct {
	Workers         int      `yaml:"workers" json:"workers"`
	ShutdownTimeout Duration `yaml:"shutdown_timeout" json:"shutdown_timeout"`
}

// AdminConfig configures VORTEX's internal admin/health HTTP server.
type AdminConfig struct {
	Address string `yaml:"address" json:"address"`
}

type LoggingConfig struct {
	Level  string `yaml:"level" json:"level"`
	Format string `yaml:"format" json:"format"`
}

type ListenerConfig struct {
	Name     string     `yaml:"name" json:"name"`
	Address  string     `yaml:"address" json:"address"`
	Protocol string     `yaml:"protocol" json:"protocol"`
	TLS      *TLSConfig `yaml:"tls,omitempty" json:"tls,omitempty"`
}

// TLSConfig enables TLS termination on an HTTP listener. TCP listeners
// don't take TLS config: they proxy raw bytes and are always passthrough
// (VORTEX never decrypts them).
type TLSConfig struct {
	Enabled     bool       `yaml:"enabled" json:"enabled"`
	Certificate string     `yaml:"certificate" json:"certificate"`
	Key         string     `yaml:"key" json:"key"`
	SNI         []SNIEntry `yaml:"sni,omitempty" json:"sni,omitempty"`
}

// SNIEntry serves a different certificate for a specific TLS server name,
// falling back to TLSConfig.Certificate/Key when no entry matches.
type SNIEntry struct {
	Host        string `yaml:"host" json:"host"`
	Certificate string `yaml:"certificate" json:"certificate"`
	Key         string `yaml:"key" json:"key"`
}

type BackendConfig struct {
	Name    string `yaml:"name" json:"name"`
	Address string `yaml:"address" json:"address"`
	Weight  int    `yaml:"weight" json:"weight"`
}

// BackendPoolConfig is a named group of backends, routed to by name from
// RouteConfig.BackendPool. Used by HTTP listeners.
type BackendPoolConfig struct {
	Name     string          `yaml:"name" json:"name"`
	Backends []BackendConfig `yaml:"backends" json:"backends"`
}

// RouteConfig maps an incoming HTTP request to a named backend pool by
// host (exact match; "" or "*" matches any host) and path (prefix match;
// the longest matching path prefix wins).
type RouteConfig struct {
	Host        string `yaml:"host" json:"host"`
	Path        string `yaml:"path" json:"path"`
	BackendPool string `yaml:"backend_pool" json:"backend_pool"`
}

type LoadBalancingConfig struct {
	Algorithm string `yaml:"algorithm" json:"algorithm"`
}

type HealthCheckConfig struct {
	Enabled            bool     `yaml:"enabled" json:"enabled"`
	Type               string   `yaml:"type" json:"type"` // "tcp" (default) or "http"
	Path               string   `yaml:"path" json:"path"` // HTTP check path, default "/health"
	Interval           Duration `yaml:"interval" json:"interval"`
	Timeout            Duration `yaml:"timeout" json:"timeout"`
	UnhealthyThreshold int      `yaml:"unhealthy_threshold" json:"unhealthy_threshold"`
	HealthyThreshold   int      `yaml:"healthy_threshold" json:"healthy_threshold"`
}

type TimeoutsConfig struct {
	Connect Duration `yaml:"connect" json:"connect"`
	Read    Duration `yaml:"read" json:"read"`
	Write   Duration `yaml:"write" json:"write"`
	Idle    Duration `yaml:"idle" json:"idle"`
}

// LimitsConfig bounds resource usage so VORTEX can't be pushed into
// unlimited memory/connection growth. Zero means "unlimited" for a field
// (opt-in caps, not surprising defaults).
type LimitsConfig struct {
	MaxConnections           int `yaml:"max_connections" json:"max_connections"`
	MaxConnectionsPerBackend int `yaml:"max_connections_per_backend" json:"max_connections_per_backend"`
	MaxRequestBodyMB         int `yaml:"max_request_body_mb" json:"max_request_body_mb"`
	MaxHeaderSizeKB          int `yaml:"max_header_size_kb" json:"max_header_size_kb"`
}

// RateLimitConfig token-bucket-limits HTTP requests globally and, if
// PerIP is set, per client IP. Disabled by default.
type RateLimitConfig struct {
	Enabled                bool    `yaml:"enabled" json:"enabled"`
	RequestsPerSecond      float64 `yaml:"requests_per_second" json:"requests_per_second"`
	Burst                  int     `yaml:"burst" json:"burst"`
	PerIP                  bool    `yaml:"per_ip" json:"per_ip"`
	PerIPRequestsPerSecond float64 `yaml:"per_ip_requests_per_second" json:"per_ip_requests_per_second"`
	PerIPBurst             int     `yaml:"per_ip_burst" json:"per_ip_burst"`
}

// CircuitBreakerConfig governs the per-backend breaker that stops sending
// traffic to a backend after repeated connection failures.
type CircuitBreakerConfig struct {
	Enabled          bool     `yaml:"enabled" json:"enabled"`
	FailureThreshold int      `yaml:"failure_threshold" json:"failure_threshold"`
	OpenTimeout      Duration `yaml:"open_timeout" json:"open_timeout"`
}

// RetryConfig governs HTTP retry-on-failure. Only safe methods (GET, HEAD,
// OPTIONS) are ever retried, regardless of config, since retrying a
// consumed request body is not generally safe.
type RetryConfig struct {
	Enabled    bool     `yaml:"enabled" json:"enabled"`
	MaxRetries int      `yaml:"max_retries" json:"max_retries"`
	RetryOn    []string `yaml:"retry_on" json:"retry_on"` // "connection_failure", "timeout"
}

// Load reads, expands environment variables in, parses, defaults, and
// validates the configuration file at path.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	expanded := os.Expand(string(raw), lookupEnv)

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	cfg.applyDefaults()

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

// lookupEnv resolves ${VAR} references in the raw config, leaving the
// original placeholder untouched when the variable is unset so missing
// overrides fail loudly instead of silently becoming an empty string.
func lookupEnv(key string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return "${" + key + "}"
}

func (c *Config) applyDefaults() {
	if c.Server.Workers <= 0 {
		c.Server.Workers = runtime.NumCPU()
	}
	if c.Server.ShutdownTimeout == 0 {
		c.Server.ShutdownTimeout = Duration(30e9) // 30s
	}
	if c.Admin.Address == "" {
		c.Admin.Address = ":9090"
	}
	if c.Logging.Level == "" {
		c.Logging.Level = "info"
	}
	if c.Logging.Format == "" {
		c.Logging.Format = "json"
	}
	if c.HealthCheck.Type == "" {
		c.HealthCheck.Type = "tcp"
	}
	if c.HealthCheck.Path == "" {
		c.HealthCheck.Path = "/health"
	}
	if c.HealthCheck.Interval == 0 {
		c.HealthCheck.Interval = Duration(5e9) // 5s
	}
	if c.HealthCheck.Timeout == 0 {
		c.HealthCheck.Timeout = Duration(2e9) // 2s
	}
	if c.HealthCheck.UnhealthyThreshold <= 0 {
		c.HealthCheck.UnhealthyThreshold = 3
	}
	if c.HealthCheck.HealthyThreshold <= 0 {
		c.HealthCheck.HealthyThreshold = 2
	}
	if c.Timeouts.Connect == 0 {
		c.Timeouts.Connect = Duration(5e9) // 5s
	}
	if c.Timeouts.Read == 0 {
		c.Timeouts.Read = Duration(30e9) // 30s
	}
	if c.Timeouts.Write == 0 {
		c.Timeouts.Write = Duration(30e9) // 30s
	}
	if c.Timeouts.Idle == 0 {
		c.Timeouts.Idle = Duration(60e9) // 60s
	}

	if c.CircuitBreaker.FailureThreshold <= 0 {
		c.CircuitBreaker.FailureThreshold = 5
	}
	if c.CircuitBreaker.OpenTimeout == 0 {
		c.CircuitBreaker.OpenTimeout = Duration(30e9) // 30s
	}

	if c.Retry.Enabled && len(c.Retry.RetryOn) == 0 {
		c.Retry.RetryOn = []string{"connection_failure", "timeout"}
	}
}

func (c *Config) validate() error {
	if c.Admin.Address == "" {
		return fmt.Errorf("admin.address must not be empty")
	}

	switch c.HealthCheck.Type {
	case "tcp", "http":
	default:
		return fmt.Errorf("health_check.type must be \"tcp\" or \"http\", got %q", c.HealthCheck.Type)
	}

	seen := make(map[string]struct{}, len(c.Listeners))
	for _, l := range c.Listeners {
		if l.Name == "" {
			return fmt.Errorf("listener missing name")
		}
		if _, dup := seen[l.Name]; dup {
			return fmt.Errorf("duplicate listener name %q", l.Name)
		}
		seen[l.Name] = struct{}{}

		if l.Address == "" {
			return fmt.Errorf("listener %q missing address", l.Name)
		}
		switch l.Protocol {
		case "http", "tcp":
		default:
			return fmt.Errorf("listener %q has unsupported protocol %q", l.Name, l.Protocol)
		}

		if l.TLS != nil && l.TLS.Enabled {
			if l.Protocol != "http" {
				return fmt.Errorf("listener %q: tls is only supported on http listeners (tcp listeners are always TLS passthrough)", l.Name)
			}
			if l.TLS.Certificate == "" || l.TLS.Key == "" {
				return fmt.Errorf("listener %q: tls.certificate and tls.key are required when tls.enabled is true", l.Name)
			}
			for _, s := range l.TLS.SNI {
				if s.Host == "" || s.Certificate == "" || s.Key == "" {
					return fmt.Errorf("listener %q: tls.sni entries require host, certificate, and key", l.Name)
				}
			}
		}
	}

	if err := validateBackends(c.Backends); err != nil {
		return fmt.Errorf("backends: %w", err)
	}

	// Backend names must be unique across the whole config (flat backends
	// plus every backend_pool), not just within one pool: the admin API and
	// vortexctl address a backend by bare name alone.
	globalBackendNames := make(map[string]struct{})
	for _, b := range c.Backends {
		globalBackendNames[b.Name] = struct{}{}
	}

	poolNames := make(map[string]struct{}, len(c.BackendPools))
	for _, p := range c.BackendPools {
		if p.Name == "" {
			return fmt.Errorf("backend_pool missing name")
		}
		if _, dup := poolNames[p.Name]; dup {
			return fmt.Errorf("duplicate backend_pool name %q", p.Name)
		}
		poolNames[p.Name] = struct{}{}

		if len(p.Backends) == 0 {
			return fmt.Errorf("backend_pool %q has no backends", p.Name)
		}
		if err := validateBackends(p.Backends); err != nil {
			return fmt.Errorf("backend_pool %q: %w", p.Name, err)
		}
		for _, b := range p.Backends {
			if _, dup := globalBackendNames[b.Name]; dup {
				return fmt.Errorf("backend name %q is used in more than one pool; names must be globally unique", b.Name)
			}
			globalBackendNames[b.Name] = struct{}{}
		}
	}

	seenRoutes := make(map[string]struct{}, len(c.Routes))
	for _, r := range c.Routes {
		if r.Path == "" || r.Path[0] != '/' {
			return fmt.Errorf("route for backend_pool %q has invalid path %q (must start with /)", r.BackendPool, r.Path)
		}
		if _, ok := poolNames[r.BackendPool]; !ok {
			return fmt.Errorf("route %s%s references unknown backend_pool %q", r.Host, r.Path, r.BackendPool)
		}
		key := r.Host + "\x00" + r.Path
		if _, dup := seenRoutes[key]; dup {
			return fmt.Errorf("duplicate route for host %q path %q", r.Host, r.Path)
		}
		seenRoutes[key] = struct{}{}
	}

	if c.Limits.MaxConnections < 0 || c.Limits.MaxConnectionsPerBackend < 0 ||
		c.Limits.MaxRequestBodyMB < 0 || c.Limits.MaxHeaderSizeKB < 0 {
		return fmt.Errorf("limits: values must not be negative")
	}

	if c.RateLimit.Enabled {
		if c.RateLimit.RequestsPerSecond <= 0 || c.RateLimit.Burst <= 0 {
			return fmt.Errorf("rate_limit: requests_per_second and burst must be > 0 when enabled")
		}
		if c.RateLimit.PerIP && (c.RateLimit.PerIPRequestsPerSecond <= 0 || c.RateLimit.PerIPBurst <= 0) {
			return fmt.Errorf("rate_limit: per_ip_requests_per_second and per_ip_burst must be > 0 when per_ip is enabled")
		}
	}

	if c.CircuitBreaker.Enabled {
		if c.CircuitBreaker.FailureThreshold <= 0 {
			return fmt.Errorf("circuit_breaker: failure_threshold must be > 0 when enabled")
		}
		if c.CircuitBreaker.OpenTimeout <= 0 {
			return fmt.Errorf("circuit_breaker: open_timeout must be > 0 when enabled")
		}
	}

	if c.Retry.Enabled {
		if c.Retry.MaxRetries < 0 {
			return fmt.Errorf("retry: max_retries must not be negative")
		}
		for _, r := range c.Retry.RetryOn {
			switch r {
			case "connection_failure", "timeout":
			default:
				return fmt.Errorf("retry: unsupported retry_on value %q", r)
			}
		}
	}

	return nil
}

func validateBackends(backends []BackendConfig) error {
	names := make(map[string]struct{}, len(backends))
	for _, b := range backends {
		if b.Name == "" {
			return fmt.Errorf("backend missing name")
		}
		if _, dup := names[b.Name]; dup {
			return fmt.Errorf("duplicate backend name %q", b.Name)
		}
		names[b.Name] = struct{}{}

		if b.Address == "" {
			return fmt.Errorf("backend %q missing address", b.Name)
		}
	}
	return nil
}
