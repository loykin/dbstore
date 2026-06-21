package store

import (
	"fmt"
	"sync"
	"time"
)

type Pool struct {
	mu      sync.Mutex
	entries map[string]*poolEntry
	driver  *DriverRegistry
}

func NewPool(registry *DriverRegistry) *Pool {
	return &Pool{
		entries: make(map[string]*poolEntry),
		driver:  registry,
	}
}

// Register adds a datasource by name and initializes its connection.
// TCP connection (including Ping) is performed outside the mutex to avoid lock contention.
func (p *Pool) Register(name string, cfg DriverConfig) error {
	p.mu.Lock()
	if _, exists := p.entries[name]; exists {
		p.mu.Unlock()
		return fmt.Errorf("dbstore: %q already registered", name)
	}
	p.mu.Unlock()

	db, err := p.driver.open(cfg)
	if err != nil {
		return fmt.Errorf("dbstore: open %q: %w", name, err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.entries[name]; exists {
		_ = db.Close()
		return fmt.Errorf("dbstore: %q already registered", name)
	}
	p.entries[name] = &poolEntry{
		db:        db,
		throttle:  newThrottle(cfg.PoolConfig.MaxConcurrency),
		createdAt: time.Now(),
	}
	return nil
}

// Remove unregisters a datasource and closes its connection pool.
// Waits for all in-flight operations to complete before closing.
func (p *Pool) Remove(name string) error {
	p.mu.Lock()
	entry, ok := p.entries[name]
	if !ok {
		p.mu.Unlock()
		return fmt.Errorf("dbstore: %q not found", name)
	}
	delete(p.entries, name)
	p.mu.Unlock()

	entry.wg.Wait() // wait for in-flight operations to finish
	return entry.db.Close()
}

// RemoveAll removes all registered datasources; call on server shutdown.
func (p *Pool) RemoveAll() {
	p.mu.Lock()
	entries := p.entries
	p.entries = make(map[string]*poolEntry)
	p.mu.Unlock()

	for _, entry := range entries {
		entry.wg.Wait()
		_ = entry.db.Close()
	}
}

// acquire returns the entry and increments the in-flight counter.
// wg.Add(1) is called under the mutex to prevent a race with Remove.
func (p *Pool) acquire(name string) (*poolEntry, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, ok := p.entries[name]
	if !ok {
		return nil, fmt.Errorf("dbstore: %q not found", name)
	}
	entry.wg.Add(1)
	return entry, nil
}

func (p *Pool) release(entry *poolEntry) {
	entry.wg.Done()
}

// get is for tests only — checks entry existence without incrementing wg.
func (p *Pool) get(name string) (*poolEntry, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, ok := p.entries[name]
	if !ok {
		return nil, fmt.Errorf("dbstore: %q not found", name)
	}
	return entry, nil
}
