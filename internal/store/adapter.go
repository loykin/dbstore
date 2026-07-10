package store

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

func (a *Adapter[T]) Executor() *Executor[T] {
	return NewExecutor(a.directory)
}

func (a *Adapter[T]) Close() {
	a.directory.RemoveAll()
}
