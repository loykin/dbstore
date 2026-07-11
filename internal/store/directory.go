package store

import (
	"fmt"
	"sync"
	"time"
)

// Closer is an optional capability a registered client may implement so Directory
// can release its resources on Remove/RemoveAll. It is checked via type
// assertion rather than required as a type constraint on Directory[T], because
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

type Directory[T any] struct {
	mu       sync.Mutex
	entries  map[string]*directoryEntry[T]
	driver   *DriverRegistry[T]
	observer Observer

	// observerMu orders Observer callback invocations to match the order
	// their data mutations were linearized by mu — see SetObserver's doc
	// comment for why a second lock is needed for this at all, instead of
	// just capturing data under mu and calling back outside it.
	observerMu sync.Mutex
}

// SetObserver registers an Observer to receive Executor[T].Run timing and
// outcome for every source, and immediately calls its ObserveSourceSnapshot
// with every source already registered — otherwise an Observer attached
// after Open would never learn those sources exist, but would still be
// told about their eventual Remove, which is exactly what makes a
// Prometheus sources_active gauge go negative.
//
// The snapshot is a single call, not a replayed ObserveSourceRegistered per
// source: calling SetObserver more than once (the same Observer value, or a
// different one) always calls ObserveSourceSnapshot again with the current
// set, and a correct Observer treats that as an idempotent state sync — see
// ObserveSourceSnapshot's doc comment. Using ObserveSourceRegistered for
// this instead was tried and reverted: it double-counted a
// registration-events counter on a second SetObserver call, and there was
// no way for an Observer to tell "genuinely new" from "already knew about
// this, just resyncing" apart from tracking its own duplicate set — pushing
// an idempotency burden onto every Observer implementation for something
// the Directory already knows how to say precisely once.
//
// Capturing p.observer/the entry set under mu is not sufficient by itself:
// two goroutines that each capture their data under mu in the correct order
// can still invoke their Observer callbacks out of that order, since nothing
// serializes the callbacks themselves once mu is released — a concurrent
// SetObserver(o) on an empty Directory racing a Register("primary") could
// capture (nil observer, no names) first under mu, correctly, and yet have
// its ObserveSourceSnapshot(nil) callback actually execute after Register's
// ObserveSourceRegistered("primary") callback, leaving a Prometheus
// sources_active gauge at 0 despite "primary" being registered. observerMu
// closes that gap: it's acquired here while still holding mu (so whichever
// goroutine's data mutation is linearized first also reserves the first
// callback slot) and released only after this call's callback(s) finish, so
// callback order always matches mutation order — the property the comment
// on Register/Remove/RemoveAll's own observerMu use depends on.
func (p *Directory[T]) SetObserver(o Observer) {
	p.mu.Lock()
	p.observer = o
	var names []string
	if len(p.entries) > 0 {
		names = make([]string, 0, len(p.entries))
		for name := range p.entries {
			names = append(names, name)
		}
	}
	p.observerMu.Lock()
	defer p.observerMu.Unlock()
	p.mu.Unlock()

	if o != nil {
		safeObserve(func() { o.ObserveSourceSnapshot(names) })
	}
}

func (p *Directory[T]) getObserver() Observer {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.observer
}

func NewDirectory[T any](registry *DriverRegistry[T]) *Directory[T] {
	return &Directory[T]{
		entries: make(map[string]*directoryEntry[T]),
		driver:  registry,
	}
}

// Register adds a datasource by name and initializes its client.
// TCP connection (including Ping) is performed outside the mutex to avoid lock contention.
func (p *Directory[T]) Register(name string, cfg SourceConfig) error {
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
	if _, exists := p.entries[name]; exists {
		p.mu.Unlock()
		_ = closeClient(client)
		return fmt.Errorf("dbstore: %q already registered", name)
	}
	p.entries[name] = &directoryEntry[T]{
		client:    client,
		throttle:  newThrottle(cfg.PoolConfig.MaxConcurrency),
		createdAt: time.Now(),
	}
	// Captured in the same critical section as the insert above, and
	// observerMu reserved before mu is released — see SetObserver's doc
	// comment for why both matter.
	observer := p.observer
	p.observerMu.Lock()
	defer p.observerMu.Unlock()
	p.mu.Unlock()

	if observer != nil {
		safeObserve(func() { observer.ObserveSourceRegistered(name) })
	}
	return nil
}

// Remove unregisters a datasource and closes its client when supported.
// Waits for all in-flight operations to complete before closing.
func (p *Directory[T]) Remove(name string) error {
	p.mu.Lock()
	entry, ok := p.entries[name]
	if !ok {
		p.mu.Unlock()
		return fmt.Errorf("dbstore: %q not found", name)
	}
	delete(p.entries, name)
	// Captured in the same critical section as the delete above, and
	// observerMu reserved before mu is released — see SetObserver's doc
	// comment for why both matter. The notification fires here, before
	// waiting for in-flight work and closing the client below, because
	// "removed" means "no longer in the registry", which is already true
	// the moment delete() runs — draining and closing are a separate
	// concern this Observer isn't reporting on.
	observer := p.observer
	p.observerMu.Lock()
	p.mu.Unlock()
	p.notifyRemoved(observer, name)

	entry.wg.Wait() // wait for in-flight operations to finish
	return closeClient(entry.client)
}

// RemoveAll removes all registered datasources; call on server shutdown.
func (p *Directory[T]) RemoveAll() {
	p.mu.Lock()
	entries := p.entries
	p.entries = make(map[string]*directoryEntry[T])
	// Captured in the same critical section as the swap above, and
	// observerMu reserved before mu is released — see SetObserver's doc
	// comment for why both matter, and Remove's for why notification
	// precedes draining/closing.
	observer := p.observer
	p.observerMu.Lock()
	p.mu.Unlock()
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	p.notifyRemoved(observer, names...)

	for _, entry := range entries {
		entry.wg.Wait()
		_ = closeClient(entry.client)
	}
}

// notifyRemoved calls observer.ObserveSourceRemoved once per name, then
// releases observerMu — via defer, so a panicking Observer still releases
// it instead of deadlocking every future SetObserver/Register/Remove call.
// Callers must already hold observerMu (and must not still hold mu) before
// calling this.
func (p *Directory[T]) notifyRemoved(observer Observer, names ...string) {
	defer p.observerMu.Unlock()
	if observer == nil {
		return
	}
	for _, name := range names {
		safeObserve(func() { observer.ObserveSourceRemoved(name) })
	}
}

// acquire returns the entry and increments the in-flight counter.
// wg.Add(1) is called under the mutex to prevent a race with Remove.
func (p *Directory[T]) acquire(name string) (*directoryEntry[T], error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, ok := p.entries[name]
	if !ok {
		return nil, fmt.Errorf("dbstore: %q not found", name)
	}
	entry.wg.Add(1)
	return entry, nil
}

func (p *Directory[T]) release(entry *directoryEntry[T]) {
	entry.wg.Done()
}

// get is for tests only — checks entry existence without incrementing wg.
func (p *Directory[T]) get(name string) (*directoryEntry[T], error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, ok := p.entries[name]
	if !ok {
		return nil, fmt.Errorf("dbstore: %q not found", name)
	}
	return entry, nil
}
