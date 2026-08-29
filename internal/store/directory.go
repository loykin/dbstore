package store

import (
	"fmt"
	"sort"
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
	closed   bool
	openings sync.WaitGroup

	// observerMu orders lifecycle callbacks to match the mutation order
	// established by mu. Run callbacks are intentionally not serialized with
	// lifecycle callbacks.
	observerMu sync.Mutex
}

// observe isolates Observer panics from the operation being observed.
func (p *Directory[T]) observe(fn func()) {
	safeObserve(fn)
}

// beginLifecycleNotification reserves the next lifecycle-callback slot while mu
// still establishes mutation order, then releases mu. Callers release
// observerMu after notification.
func (p *Directory[T]) beginLifecycleNotification() Observer {
	observer := p.observer
	p.observerMu.Lock()
	p.mu.Unlock()
	return observer
}

func NewDirectory[T any](registry *DriverRegistry[T], observer Observer) *Directory[T] {
	return &Directory[T]{
		entries:  make(map[string]*directoryEntry[T]),
		driver:   registry,
		observer: observer,
	}
}

// Register adds a non-empty datasource name and initializes its client.
// TCP connection (including Ping) is performed outside the mutex to avoid lock contention.
func (p *Directory[T]) Register(name string, cfg SourceConfig) error {
	_, err := p.register(name, cfg)
	return err
}

// register is Register's identity-returning form. The returned entry is used
// by Adapter.Configure to roll back only the exact source instance that call
// opened, even if another goroutine removes and replaces the same name before
// rollback starts.
func (p *Directory[T]) register(name string, cfg SourceConfig) (*directoryEntry[T], error) {
	if name == "" {
		return nil, fmt.Errorf("dbstore: source name is required")
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, fmt.Errorf("dbstore: directory is closed")
	}
	if _, exists := p.entries[name]; exists {
		p.mu.Unlock()
		return nil, fmt.Errorf("dbstore: %q already registered", name)
	}
	// register is the single transition from construction to runtime. Freeze
	// driver configuration here rather than in every Adapter entry point so
	// Open, Configure, and direct Directory use cannot drift in behavior.
	p.driver.Freeze()
	// Add is serialized with RemoveAll setting closed under the same mutex.
	// Consequently no Add can race a Wait that begins after closed is set.
	p.openings.Add(1)
	p.mu.Unlock()
	defer p.openings.Done()

	client, err := p.driver.open(cfg)
	if err != nil {
		return nil, fmt.Errorf("dbstore: open %q: %w", name, err)
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		_ = closeClient(client)
		return nil, fmt.Errorf("dbstore: directory is closed")
	}
	if _, exists := p.entries[name]; exists {
		p.mu.Unlock()
		_ = closeClient(client)
		return nil, fmt.Errorf("dbstore: %q already registered", name)
	}
	entry := &directoryEntry[T]{
		client:         client,
		driver:         cfg.Driver,
		throttle:       newThrottle(cfg.PoolConfig.MaxConcurrency),
		maxConcurrency: cfg.PoolConfig.MaxConcurrency,
		createdAt:      time.Now(),
	}
	p.entries[name] = entry
	// Reserve lifecycle callback order in the same critical section as the
	// mutation, then notify without holding the entries lock.
	observer := p.beginLifecycleNotification()
	defer p.observerMu.Unlock()

	if observer != nil {
		p.observe(func() { observer.ObserveSourceRegistered(name) })
	}
	return entry, nil
}

// Sources returns a deterministic, redacted snapshot of the currently
// registered sources. No DSN or client value is retained in SourceInfo.
func (p *Directory[T]) Sources() []SourceInfo {
	p.mu.Lock()
	defer p.mu.Unlock()

	sources := make([]SourceInfo, 0, len(p.entries))
	for name, entry := range p.entries {
		sources = append(sources, SourceInfo{
			Name:           name,
			Driver:         entry.driver,
			CreatedAt:      entry.createdAt,
			MaxConcurrency: entry.maxConcurrency,
		})
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Name < sources[j].Name })
	return sources
}

// Remove unregisters a datasource and closes its client when supported.
// Waits for all in-flight operations to complete before closing.
func (p *Directory[T]) Remove(name string) error {
	return p.removeEntry(name, nil)
}

// removeEntry removes name only when it still refers to expected. A nil
// expected preserves Remove's public name-based behavior. Identity-checked
// removal is used by Configure rollback to avoid deleting a replacement that
// another goroutine registered under the same name.
func (p *Directory[T]) removeEntry(name string, expected *directoryEntry[T]) error {
	p.mu.Lock()
	entry, ok := p.entries[name]
	if !ok || (expected != nil && entry != expected) {
		p.mu.Unlock()
		if expected != nil {
			return nil
		}
		return fmt.Errorf("dbstore: %q not found", name)
	}
	delete(p.entries, name)
	// Reserve lifecycle callback order in the same critical section as the
	// delete. The notification fires before waiting for in-flight work and closing the client
	// below, because "removed" means "no longer in the registry", which is
	// already true the moment delete() runs — draining and closing are a
	// separate concern this Observer isn't reporting on.
	observer := p.beginLifecycleNotification()
	p.notifyRemoved(observer, name)

	entry.wg.Wait() // wait for in-flight operations to finish
	return closeClient(entry.client)
}

// RemoveAll permanently closes the Directory and all registered datasources;
// call it on server shutdown. Registers already opening are drained and their
// clients closed, and future Register calls fail.
func (p *Directory[T]) RemoveAll() {
	p.mu.Lock()
	p.closed = true
	entries := p.entries
	p.entries = make(map[string]*directoryEntry[T])
	// Reserve lifecycle callback order in the same critical section as the
	// swap. Notification precedes draining and closing.
	observer := p.beginLifecycleNotification()
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	p.notifyRemoved(observer, names...)

	for _, entry := range entries {
		entry.wg.Wait()
		_ = closeClient(entry.client)
	}

	// Register performs driver Open outside mu. Wait for every Open that began
	// before closed was set; each one rechecks closed and closes its own client
	// instead of publishing it. RemoveAll therefore cannot return while a late
	// source can still appear or an opened client still needs cleanup.
	p.openings.Wait()
}

// notifyRemoved calls observer.ObserveSourceRemoved once per name, then
// releases observerMu — via defer, so a panicking Observer still releases
// it instead of deadlocking future Register/Remove calls.
// Callers must already hold observerMu (and must not still hold mu) before
// calling this.
func (p *Directory[T]) notifyRemoved(observer Observer, names ...string) {
	defer p.observerMu.Unlock()
	if observer == nil {
		return
	}
	for _, name := range names {
		p.observe(func() { observer.ObserveSourceRemoved(name) })
	}
}

// sourceEntry snapshots the current identity behind name without admitting an
// operation. Source uses this once at construction so later same-name
// registrations cannot silently retarget it.
func (p *Directory[T]) sourceEntry(name string) *directoryEntry[T] {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.entries[name]
}

// acquire returns the current entry and increments the in-flight counter.
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

// acquireSource admits work only when name still refers to the exact entry a
// Source captured at construction. A nil or replaced entry is rejected rather
// than falling back to a live name lookup.
func (p *Directory[T]) acquireSource(name string, expected *directoryEntry[T]) (*directoryEntry[T], error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, ok := p.entries[name]
	if !ok || expected == nil || entry != expected {
		return nil, fmt.Errorf("dbstore: source %q is no longer registered", name)
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
