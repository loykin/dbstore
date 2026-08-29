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
	Remove(name string) error
	Sources() []SourceInfo
	Executor() *Executor[T]
	Close()
}

type Adapter[T any] struct {
	registry  *DriverRegistry[T]
	directory *Directory[T]
}

var _ AdapterContract[any] = (*Adapter[any])(nil)

type adapterOptions struct {
	observer    Observer
	observerSet bool
}

// AdapterOption configures construction-time decisions. Options are applied
// once by NewAdapter and are never consulted on the operation path.
type AdapterOption func(*adapterOptions)

// WithObserver fixes observer as the Adapter's Observer at construction.
func WithObserver(observer Observer) AdapterOption {
	return func(options *adapterOptions) {
		if options.observerSet {
			panic("dbstore: Observer configured more than once")
		}
		options.observer = observer
		options.observerSet = true
	}
}

func NewAdapter[T any](options ...AdapterOption) *Adapter[T] {
	var configured adapterOptions
	for _, option := range options {
		if option != nil {
			option(&configured)
		}
	}
	registry := NewDriverRegistry[T]()
	return &Adapter[T]{
		registry:  registry,
		directory: NewDirectory(registry, configured.observer),
	}
}

// RegisterDriver adds a uniquely named driver during setup. The first valid
// source registration freezes the registry so runtime source operations cannot
// race configuration changes.
func (a *Adapter[T]) RegisterDriver(name string, driver DriverBuilder[T]) {
	a.registry.Register(name, driver)
}

// Open registers and connects a single named source. name is supplied by
// the caller, not cfg — SourceConfig has no name field, because the name is
// a fixed identifier repository code already references (e.g. via
// Executor.Run), not a value that should vary with environment config. For
// opening several sources at once, see Configure, where the same name
// lives as the map key instead of a positional argument. An empty name is
// rejected consistently by both Open and Configure.
func (a *Adapter[T]) Open(name string, cfg SourceConfig) error {
	return a.directory.Register(name, cfg)
}

// Configure is Open for every entry in cfg.Sources, keyed by name the same
// way Open takes name as a parameter — SourceConfig itself never carries a
// name. It publishes sequentially, not atomically: sources are opened one
// at a time, and if any fails, every source this call already opened is
// closed again before the error is returned — but a source opened earlier
// in the same call is genuinely visible to a concurrent Run before that
// rollback runs. A rollback Remove's own error is discarded; only the
// triggering Open error is returned. A name already registered by an
// earlier call is left untouched — only names this call opened are rolled
// back.
func (a *Adapter[T]) Configure(cfg Config) error {
	if _, ok := cfg.Sources[""]; ok {
		return fmt.Errorf("configure source: name is required")
	}

	type openedSource struct {
		name  string
		entry *directoryEntry[T]
	}
	opened := make([]openedSource, 0, len(cfg.Sources))
	for name, source := range cfg.Sources {
		entry, err := a.directory.register(name, source)
		if err != nil {
			for _, openedSource := range opened {
				_ = a.directory.removeEntry(openedSource.name, openedSource.entry)
			}
			return fmt.Errorf("configure source %q: %w", name, err)
		}
		opened = append(opened, openedSource{name: name, entry: entry})
	}
	return nil
}

// Remove unregisters a single named source, waiting for its in-flight Run
// calls to finish and closing its client, without touching any other
// source. This is the entry point the "Dynamic Sources" pattern needs for
// per-tenant teardown — Adapter has no other way to reach a single source
// once opened, since Open only ever adds.
func (a *Adapter[T]) Remove(name string) error {
	return a.directory.Remove(name)
}

// Sources returns redacted metadata for every currently registered source.
func (a *Adapter[T]) Sources() []SourceInfo {
	return a.directory.Sources()
}

func (a *Adapter[T]) Executor() *Executor[T] {
	return NewExecutor(a.directory)
}

// Close permanently shuts down the Adapter. It waits for in-flight Runs and
// Opens to finish cleaning up; subsequent Open calls fail.
func (a *Adapter[T]) Close() {
	a.directory.RemoveAll()
}
