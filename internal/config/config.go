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
	Server        ServerConfig        `yaml:"server"`
	Admin         AdminConfig         `yaml:"admin"`
	Logging       LoggingConfig       `yaml:"logging"`
	Listeners     []ListenerConfig    `yaml:"listeners"`
	Backends      []BackendConfig     `yaml:"backends"` // flat pool used by TCP listeners
	BackendPools  []BackendPoolConfig `yaml:"backend_pools"`
	Routes        []RouteConfig       `yaml:"routes"`
	LoadBalancing LoadBalancingConfig `yaml:"load_balancing"`
	HealthCheck   HealthCheckConfig   `yaml:"health_check"`
	Timeouts      TimeoutsConfig      `yaml:"timeouts"`
}

type ServerConfig struct {
	Workers         int      `yaml:"workers"`
	ShutdownTimeout Duration `yaml:"shutdown_timeout"`
}

// AdminConfig configures VORTEX's internal admin/health HTTP server.
type AdminConfig struct {
	Address string `yaml:"address"`
}

type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

type ListenerConfig struct {
	Name     string `yaml:"name"`
	Address  string `yaml:"address"`
	Protocol string `yaml:"protocol"`
}

type BackendConfig struct {
	Name    string `yaml:"name"`
	Address string `yaml:"address"`
	Weight  int    `yaml:"weight"`
}

// BackendPoolConfig is a named group of backends, routed to by name from
// RouteConfig.BackendPool. Used by HTTP listeners.
type BackendPoolConfig struct {
	Name     string          `yaml:"name"`
	Backends []BackendConfig `yaml:"backends"`
}

// RouteConfig maps an incoming HTTP request to a named backend pool by
// host (exact match; "" or "*" matches any host) and path (prefix match;
// the longest matching path prefix wins).
type RouteConfig struct {
	Host        string `yaml:"host"`
	Path        string `yaml:"path"`
	BackendPool string `yaml:"backend_pool"`
}

type LoadBalancingConfig struct {
	Algorithm string `yaml:"algorithm"`
}

type HealthCheckConfig struct {
	Enabled            bool     `yaml:"enabled"`
	Type               string   `yaml:"type"` // "tcp" (default) or "http"
	Path               string   `yaml:"path"` // HTTP check path, default "/health"
	Interval           Duration `yaml:"interval"`
	Timeout            Duration `yaml:"timeout"`
	UnhealthyThreshold int      `yaml:"unhealthy_threshold"`
	HealthyThreshold   int      `yaml:"healthy_threshold"`
}

type TimeoutsConfig struct {
	Connect Duration `yaml:"connect"`
	Read    Duration `yaml:"read"`
	Write   Duration `yaml:"write"`
	Idle    Duration `yaml:"idle"`
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
	}

	if err := validateBackends(c.Backends); err != nil {
		return fmt.Errorf("backends: %w", err)
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
