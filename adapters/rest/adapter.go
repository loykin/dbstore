package restadapter

import "github.com/loykin/dbstore"

type Adapter struct {
	core *dbstore.Adapter[*Client]
}

var _ dbstore.AdapterContract[*Client] = (*Adapter)(nil)

func New() *Adapter {
	return &Adapter{core: dbstore.NewAdapter[*Client]()}
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

func (a *Adapter) SetObserver(o dbstore.Observer) {
	a.core.SetObserver(o)
}

func (a *Adapter) Close() {
	a.core.Close()
}
