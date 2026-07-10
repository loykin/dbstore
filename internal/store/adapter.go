package store

import "fmt"

type Adapter[T any] struct {
	registry  *DriverRegistry[T]
	directory *Directory[T]
}

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

func (a *Adapter[T]) Open(name string, cfg SourceConfig) error {
	return a.directory.Register(name, cfg)
}

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

func (a *Adapter[T]) Close() {
	a.directory.RemoveAll()
}
