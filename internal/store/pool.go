package store

import (
	"fmt"
	"sync"
	"time"
)

// Closer is an optional capability a pooled client may implement so Pool
// can release its resources on Remove/RemoveAll. It is checked via type
// assertion rather than required as a type constraint on Pool[T], because
// not every client exposes an explicit close — e.g. an HTTP-based client
// like opensearch-go has no Close() method at all.
type Closer interface {
	Close() error
}

func closeClient[T any](client T) error {
	if c, ok := any(client).(Closer); ok {
		return c.Close()
	}
	return nil
}

type Pool[T any] struct {
	mu      sync.Mutex
	entries map[string]*poolEntry[T]
	driver  *DriverRegistry[T]
}

func NewPool[T any](registry *DriverRegistry[T]) *Pool[T] {
	return &Pool[T]{
		entries: make(map[string]*poolEntry[T]),
		driver:  registry,
	}
}

// Register adds a datasource by name and initializes its connection.
// TCP connection (including Ping) is performed outside the mutex to avoid lock contention.
func (p *Pool[T]) Register(name string, cfg DriverConfig) error {
	p.mu.Lock()
	if _, exists := p.entries[name]; exists {
		p.mu.Unlock()
		return fmt.Errorf("dbstore: %q already registered", name)
	}
	p.mu.Unlock()

	client, err := p.driver.open(cfg)
	if err != nil {
		return fmt.Errorf("dbstore: open %q: %w", name, err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.entries[name]; exists {
		_ = closeClient(client)
		return fmt.Errorf("dbstore: %q already registered", name)
	}
	p.entries[name] = &poolEntry[T]{
		client:    client,
		throttle:  newThrottle(cfg.PoolConfig.MaxConcurrency),
		createdAt: time.Now(),
	}
	return nil
}

// Remove unregisters a datasource and closes its connection pool.
// Waits for all in-flight operations to complete before closing.
func (p *Pool[T]) Remove(name string) error {
	p.mu.Lock()
	entry, ok := p.entries[name]
	if !ok {
		p.mu.Unlock()
		return fmt.Errorf("dbstore: %q not found", name)
	}
	delete(p.entries, name)
	p.mu.Unlock()

	entry.wg.Wait() // wait for in-flight operations to finish
	return closeClient(entry.client)
}

// RemoveAll removes all registered datasources; call on server shutdown.
func (p *Pool[T]) RemoveAll() {
	p.mu.Lock()
	entries := p.entries
	p.entries = make(map[string]*poolEntry[T])
	p.mu.Unlock()

	for _, entry := range entries {
		entry.wg.Wait()
		_ = closeClient(entry.client)
	}
}

// acquire returns the entry and increments the in-flight counter.
// wg.Add(1) is called under the mutex to prevent a race with Remove.
func (p *Pool[T]) acquire(name string) (*poolEntry[T], error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, ok := p.entries[name]
	if !ok {
		return nil, fmt.Errorf("dbstore: %q not found", name)
	}
	entry.wg.Add(1)
	return entry, nil
}

func (p *Pool[T]) release(entry *poolEntry[T]) {
	entry.wg.Done()
}

// get is for tests only — checks entry existence without incrementing wg.
func (p *Pool[T]) get(name string) (*poolEntry[T], error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, ok := p.entries[name]
	if !ok {
		return nil, fmt.Errorf("dbstore: %q not found", name)
	}
	return entry, nil
}
