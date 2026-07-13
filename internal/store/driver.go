package store

import (
	"fmt"
	"sync"
)

// DriverBuilder opens a new client of type T from cfg.
type DriverBuilder[T any] interface {
	Open(cfg SourceConfig) (T, error)
}

// PoolConfigApplier is an optional capability a DriverBuilder can implement
// to tune client settings right after Open. It is kept separate from
// DriverBuilder because not every backend client has pool settings.
type PoolConfigApplier[T any] interface {
	ApplyPoolConfig(client T, cfg PoolConfig)
}

type DriverRegistry[T any] struct {
	mu       sync.RWMutex
	builders map[string]DriverBuilder[T]
}

func NewDriverRegistry[T any]() *DriverRegistry[T] {
	return &DriverRegistry[T]{builders: make(map[string]DriverBuilder[T])}
}

func (r *DriverRegistry[T]) Register(name string, b DriverBuilder[T]) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.builders[name] = b
}

func (r *DriverRegistry[T]) open(cfg SourceConfig) (T, error) {
	var zero T
	r.mu.RLock()
	b, ok := r.builders[cfg.Driver]
	r.mu.RUnlock()
	if !ok {
		return zero, fmt.Errorf("dbstore: unknown driver %q", cfg.Driver)
	}
	client, err := b.Open(cfg)
	if err != nil {
		return zero, err
	}
	// Asserted on the builder b, not the freshly-opened client: it's the
	// builder (author of Open) that optionally knows how to tune the client
	// it just produced, mirroring closeClient's assertion on the client
	// itself for the opposite (teardown) capability in directory.go.
	if applier, ok := any(b).(PoolConfigApplier[T]); ok {
		applier.ApplyPoolConfig(client, cfg.PoolConfig)
	}
	return client, nil
}
