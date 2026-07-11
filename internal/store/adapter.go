package store

import "fmt"

// AdapterContract is the method set every adapter package (sqlxadapter,
// restadapter, opensearchadapter, elasticsearchadapter, ...) is expected to
// expose by wrapping Adapter[T]. Adapter packages compose rather than embed
// Adapter[T] to keep unrelated core methods from leaking into their public
// surface, which means nothing in the type system previously caught a
// package that forgot a method or drifted on a signature — asserting
// against this interface does.
type AdapterContract[T any] interface {
	RegisterDriver(name string, driver DriverBuilder[T])
	Open(name string, cfg SourceConfig) error
	Configure(cfg Config) error
	Executor() *Executor[T]
	SetObserver(o Observer)
	Close()
}

type Adapter[T any] struct {
	registry  *DriverRegistry[T]
	directory *Directory[T]
}

var _ AdapterContract[any] = (*Adapter[any])(nil)

func NewAdapter[T any]() *Adapter[T] {
	registry := NewDriverRegistry[T]()
	return &Adapter[T]{
		registry:  registry,
		directory: NewDirectory(registry),
	}
}

func (a *Adapter[T]) RegisterDriver(name string, driver DriverBuilder[T]) {
	a.registry.Register(name, driver)
}

// Open registers and connects a single named source. name is supplied by
// the caller, not cfg — SourceConfig has no name field, because the name is
// a fixed identifier repository code already references (e.g. via
// Executor.Run), not a value that should vary with environment config. For
// opening several sources at once, see Configure, where the same name
// lives as the map key instead of a positional argument.
func (a *Adapter[T]) Open(name string, cfg SourceConfig) error {
	return a.directory.Register(name, cfg)
}

// Configure is Open for every entry in cfg.Sources, keyed by name the same
// way Open takes name as a parameter — SourceConfig itself never carries a
// name. It is all-or-nothing: if any source fails to open, every source
// already opened by this call is closed again before the error is returned.
func (a *Adapter[T]) Configure(cfg Config) error {
	if _, ok := cfg.Sources[""]; ok {
		return fmt.Errorf("configure source: name is required")
	}

	opened := make([]string, 0, len(cfg.Sources))
	for name, source := range cfg.Sources {
		if err := a.Open(name, source); err != nil {
			for _, openedName := range opened {
				_ = a.directory.Remove(openedName)
			}
			return fmt.Errorf("configure source %q: %w", name, err)
		}
		opened = append(opened, name)
	}
	return nil
}

func (a *Adapter[T]) Executor() *Executor[T] {
	return NewExecutor(a.directory)
}

// SetObserver registers an Observer that every Executor this Adapter has
// already returned, or will return, notifies on each Run call — Executors
// read the Directory's Observer at call time, not at construction, so this
// takes effect immediately, including for Executors obtained earlier.
func (a *Adapter[T]) SetObserver(o Observer) {
	a.directory.SetObserver(o)
}

func (a *Adapter[T]) Close() {
	a.directory.RemoveAll()
}
