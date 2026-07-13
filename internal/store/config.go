package store

import "time"

type SourceConfig struct {
	Driver     string     `json:"driver" yaml:"driver"`
	DSN        string     `json:"dsn" yaml:"dsn"`
	PoolConfig PoolConfig `json:"pool,omitempty" yaml:"pool,omitempty"`
}

// Config is a batch of named sources, typically decoded from JSON/YAML. The
// map key is the source name — the same identifier application code passes
// to Executor.Run — not an environment-configurable value, so it lives as
// the map key rather than a duplicated field on SourceConfig.
type Config struct {
	Sources map[string]SourceConfig `json:"sources" yaml:"sources"`
}

// SourceInfo is a redacted snapshot of one registered source. It deliberately
// excludes the DSN and all driver-specific client state so callers can expose
// it through diagnostics or management APIs without leaking credentials.
type SourceInfo struct {
	Name           string    `json:"name"`
	Driver         string    `json:"driver"`
	CreatedAt      time.Time `json:"createdAt"`
	MaxConcurrency int       `json:"maxConcurrency"`
}

// PoolConfig configures both the SQL connection pool and the per-datasource
// throttle. MaxOpenConns/MaxIdleConns/MaxLifetime/MaxIdleTime only take
// effect if the driver implements PoolConfigApplier[T] (see driver.go) —
// non-SQL drivers (e.g. an HTTP-based client) typically skip that interface
// and those four fields go unused. MaxConcurrency is the exception:
// Directory[T] applies it directly via Throttle regardless of driver, so it's the only
// field every backend actually respects.
//
// For SQL backends, MaxOpenConns and MaxConcurrency are two independent
// concurrency limits stacked on top of each other (database/sql's pool, and
// dbstore's throttle). Keep MaxOpenConns >= MaxConcurrency so the throttle is
// always the one place that can block — otherwise a request that already
// cleared the throttle can still queue invisibly inside database/sql's pool,
// and a ctx timeout no longer tells you which layer it happened in.
// sqlxadapter.ApplyPoolConfig logs a warning when this ratio is violated.
type PoolConfig struct {
	MaxOpenConns   int           `json:"maxOpenConns,omitempty" yaml:"maxOpenConns,omitempty"`
	MaxIdleConns   int           `json:"maxIdleConns,omitempty" yaml:"maxIdleConns,omitempty"`
	MaxLifetime    time.Duration `json:"maxLifetime,omitempty" yaml:"maxLifetime,omitempty"`
	MaxIdleTime    time.Duration `json:"maxIdleTime,omitempty" yaml:"maxIdleTime,omitempty"`
	MaxConcurrency int           `json:"maxConcurrency,omitempty" yaml:"maxConcurrency,omitempty"`
}

var DefaultPoolConfig = PoolConfig{
	MaxOpenConns:   10,
	MaxIdleConns:   2,
	MaxLifetime:    30 * time.Minute,
	MaxIdleTime:    5 * time.Minute,
	MaxConcurrency: 5,
}
