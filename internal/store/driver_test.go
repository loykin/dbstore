package store

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

type intDriver int

func (d intDriver) Open(SourceConfig) (int, error) { return int(d), nil }

func TestDriverRegistry_ConcurrentOpen(t *testing.T) {
	registry := NewDriverRegistry[int]()
	registry.Register("driver", intDriver(0))

	const workers = 100
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
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

func TestDriverRegistry_RegisterRejectsDuplicateName(t *testing.T) {
	registry := NewDriverRegistry[int]()
	registry.Register("driver", intDriver(1))

	assert.PanicsWithValue(t,
		`dbstore: driver "driver" already registered`,
		func() { registry.Register("driver", intDriver(2)) },
	)
}

func TestDriverRegistry_RegisterPanicsAfterFreeze(t *testing.T) {
	registry := NewDriverRegistry[int]()
	registry.Register("driver", intDriver(1))
	registry.Freeze()

	assert.PanicsWithValue(t,
		"dbstore: driver registration after source opening began",
		func() { registry.Register("late", intDriver(2)) },
	)
}

func TestDirectory_FirstRegisterFreezesDriverRegistry(t *testing.T) {
	registry := NewDriverRegistry[int]()
	registry.Register("driver", intDriver(1))
	directory := NewDirectory(registry, nil)
	defer directory.RemoveAll()

	assert.NoError(t, directory.Register("primary", SourceConfig{Driver: "driver"}))
	assert.PanicsWithValue(t,
		"dbstore: driver registration after source opening began",
		func() { registry.Register("late", intDriver(2)) },
	)
}

func TestDirectory_FailedFirstRegisterStillFreezesDriverRegistry(t *testing.T) {
	registry := NewDriverRegistry[int]()
	directory := NewDirectory(registry, nil)
	defer directory.RemoveAll()

	err := directory.Register("primary", SourceConfig{Driver: "missing"})
	assert.ErrorContains(t, err, `unknown driver "missing"`)
	assert.PanicsWithValue(t,
		"dbstore: driver registration after source opening began",
		func() { registry.Register("late", intDriver(2)) },
	)
}
