package restadapter

import "github.com/loykin/dbstore"

type Adapter struct {
	core *dbstore.Adapter[*Client]
}

type Option = dbstore.AdapterOption

var _ dbstore.AdapterContract[*Client] = (*Adapter)(nil)

func New(options ...Option) *Adapter {
	return &Adapter{core: dbstore.NewAdapter[*Client](options...)}
}

func (a *Adapter) RegisterDriver(name string, driver dbstore.DriverBuilder[*Client]) {
	a.core.RegisterDriver(name, driver)
}

func (a *Adapter) Open(name string, cfg dbstore.SourceConfig) error {
	return a.core.Open(name, cfg)
}

func (a *Adapter) Configure(cfg dbstore.Config) error {
	return a.core.Configure(cfg)
}

func (a *Adapter) Remove(name string) error {
	return a.core.Remove(name)
}

func (a *Adapter) Sources() []dbstore.SourceInfo {
	return a.core.Sources()
}

func (a *Adapter) Executor() *dbstore.Executor[*Client] {
	return a.core.Executor()
}

// Source returns a Runner[Handle] scoped to name — see the sqlxadapter
// equivalent for why this isn't part of AdapterContract[T].
func (a *Adapter) Source(name string) Source {
	return NewSource(name, a.core.Executor())
}

func (a *Adapter) Close() {
	a.core.Close()
}
