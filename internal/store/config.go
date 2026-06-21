package store

import "time"

type DriverConfig struct {
	Driver     string
	DSN        string
	PoolConfig PoolConfig
}

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
