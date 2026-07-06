package store

import "time"

type DriverConfig struct {
	Driver     string
	DSN        string
	PoolConfig PoolConfig
}

// PoolConfig configures both the SQL connection pool and the per-datasource
// throttle. MaxOpenConns/MaxIdleConns/MaxLifetime/MaxIdleTime only take
// effect if the driver implements PoolConfigApplier[T] (see driver.go) —
// non-SQL drivers (e.g. an HTTP-based client) typically skip that interface
// and those four fields go unused. MaxConcurrency is the exception: Pool[T]
// applies it directly via Throttle regardless of driver, so it's the only
// field every backend actually respects.
type PoolConfig struct {
	MaxOpenConns   int
	MaxIdleConns   int
	MaxLifetime    time.Duration
	MaxIdleTime    time.Duration
	MaxConcurrency int
}

var DefaultPoolConfig = PoolConfig{
	MaxOpenConns:   10,
	MaxIdleConns:   2,
	MaxLifetime:    30 * time.Minute,
	MaxIdleTime:    5 * time.Minute,
	MaxConcurrency: 5,
}
