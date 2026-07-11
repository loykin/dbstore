package opensearchadapter

import (
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"

	"github.com/loykin/dbstore"
)

type Adapter struct {
	core *dbstore.Adapter[*opensearchapi.Client]
}

var _ dbstore.AdapterContract[*opensearchapi.Client] = (*Adapter)(nil)

func New() *Adapter {
	return &Adapter{core: dbstore.NewAdapter[*opensearchapi.Client]()}
}

func (a *Adapter) RegisterDriver(name string, driver dbstore.DriverBuilder[*opensearchapi.Client]) {
	a.core.RegisterDriver(name, driver)
}

func (a *Adapter) Open(name string, cfg dbstore.SourceConfig) error {
	return a.core.Open(name, cfg)
}

func (a *Adapter) Configure(cfg dbstore.Config) error {
	return a.core.Configure(cfg)
}

func (a *Adapter) Executor() *dbstore.Executor[*opensearchapi.Client] {
	return a.core.Executor()
}

func (a *Adapter) SetObserver(o dbstore.Observer) {
	a.core.SetObserver(o)
}

func (a *Adapter) Close() {
	a.core.Close()
}
