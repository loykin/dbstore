package store

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type lifecycleTestClient struct {
	closed atomic.Int32
}

func (c *lifecycleTestClient) Close() error {
	c.closed.Add(1)
	return nil
}

type blockingLifecycleTestDriver struct {
	started chan struct{}
	release chan struct{}
	client  *lifecycleTestClient
}

func (d *blockingLifecycleTestDriver) Open(SourceConfig) (*lifecycleTestClient, error) {
	close(d.started)
	<-d.release
	return d.client, nil
}

func TestDirectory_Register(t *testing.T) {
	tests := []struct {
		name    string
		dsn     string
		wantErr bool
	}{
		{"valid sqlite", ":memory:", false},
		{"file path", "file::memory:?cache=shared", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := newTestDirectory()
			defer pool.RemoveAll()

			err := pool.Register("db", testConfig(tt.dsn))
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestDirectory_Register_Duplicate(t *testing.T) {
	pool := newTestDirectory()
	defer pool.RemoveAll()

	require.NoError(t, pool.Register("primary", testConfig(":memory:")))

	err := pool.Register("primary", testConfig(":memory:"))
	assert.Error(t, err)
}

func TestDirectory_Register_UnknownDriver(t *testing.T) {
	pool := NewDirectory(NewDriverRegistry[*sqlx.DB](), nil)
	defer pool.RemoveAll()

	err := pool.Register("db", SourceConfig{Driver: "unknown", DSN: ":memory:"})
	assert.Error(t, err)
}

func TestDirectory_Register_RequiresName(t *testing.T) {
	pool := newTestDirectory()
	defer pool.RemoveAll()

	err := pool.Register("", testConfig(":memory:"))
	require.Error(t, err)
	require.ErrorContains(t, err, "source name is required")
}

func TestDirectory_Remove(t *testing.T) {
	pool := newTestDirectory()

	require.NoError(t, pool.Register("primary", testConfig(":memory:")))
	require.NoError(t, pool.Remove("primary"))

	_, err := pool.get("primary")
	assert.Error(t, err)
}

func TestDirectory_Remove_NotFound(t *testing.T) {
	pool := newTestDirectory()

	err := pool.Remove("nonexistent")
	assert.Error(t, err)
}

func TestDirectory_RemoveAll(t *testing.T) {
	pool := newTestDirectory()

	require.NoError(t, pool.Register("primary", testConfig(":memory:")))
	require.NoError(t, pool.Register("analytics", testConfig(":memory:")))

	pool.RemoveAll()

	_, err := pool.get("primary")
	assert.Error(t, err)
	_, err = pool.get("analytics")
	assert.Error(t, err)
}

func TestDirectory_RemoveAllWaitsForOpeningAndPreventsLatePublish(t *testing.T) {
	client := &lifecycleTestClient{}
	driver := &blockingLifecycleTestDriver{
		started: make(chan struct{}),
		release: make(chan struct{}),
		client:  client,
	}
	registry := NewDriverRegistry[*lifecycleTestClient]()
	registry.Register("blocking", driver)
	directory := NewDirectory(registry, nil)
	var releaseOnce atomic.Bool
	releaseDriver := func() {
		if releaseOnce.CompareAndSwap(false, true) {
			close(driver.release)
		}
	}
	defer releaseDriver()

	registerDone := make(chan error, 1)
	go func() {
		registerDone <- directory.Register("late", SourceConfig{Driver: "blocking"})
	}()
	<-driver.started

	closeDone := make(chan struct{})
	go func() {
		directory.RemoveAll()
		close(closeDone)
	}()

	require.Eventually(t, func() bool {
		directory.mu.Lock()
		defer directory.mu.Unlock()
		return directory.closed
	}, time.Second, time.Millisecond)
	select {
	case <-closeDone:
		t.Fatal("RemoveAll returned while Driver.Open was still in progress")
	default:
	}

	releaseDriver()
	require.ErrorContains(t, <-registerDone, "directory is closed")
	<-closeDone
	assert.Empty(t, directory.Sources())
	assert.Equal(t, int32(1), client.closed.Load())
	require.ErrorContains(t,
		directory.Register("after-close", SourceConfig{Driver: "blocking"}),
		"directory is closed",
	)
}

func TestDirectory_SourcesReturnsSortedRedactedMetadata(t *testing.T) {
	pool := newTestDirectory()
	defer pool.RemoveAll()

	first := testConfig("file:first?mode=memory&cache=shared")
	first.PoolConfig.MaxConcurrency = 3
	require.NoError(t, pool.Register("zeta", first))
	require.NoError(t, pool.Register("alpha", testConfig("file:alpha?mode=memory&cache=shared")))

	sources := pool.Sources()
	require.Len(t, sources, 2)
	assert.Equal(t, "alpha", sources[0].Name)
	assert.Equal(t, "sqlite", sources[0].Driver)
	assert.False(t, sources[0].CreatedAt.IsZero())
	assert.Equal(t, "zeta", sources[1].Name)
	assert.Equal(t, 3, sources[1].MaxConcurrency)
}

func TestDirectory_Get_AfterRemove(t *testing.T) {
	pool := newTestDirectory()

	require.NoError(t, pool.Register("primary", testConfig(":memory:")))
	require.NoError(t, pool.Remove("primary"))

	_, err := pool.get("primary")
	assert.Error(t, err)
}
