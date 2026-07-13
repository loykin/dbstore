package store

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

type intDriver int

func (d intDriver) Open(SourceConfig) (int, error) { return int(d), nil }

func TestDriverRegistry_ConcurrentRegisterAndOpen(t *testing.T) {
	registry := NewDriverRegistry[int]()
	registry.Register("driver", intDriver(0))

	const workers = 100
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			registry.Register("driver", intDriver(i))
		}(i)
		go func() {
			defer wg.Done()
			_, err := registry.open(SourceConfig{Driver: "driver"})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		assert.NoError(t, err)
	}
}
