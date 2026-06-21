package store

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestThrottle_NoLimit(t *testing.T) {
	th := newThrottle(0)

	for i := 0; i < 100; i++ {
		require.NoError(t, th.Acquire(context.Background()))
	}
}

func TestThrottle_Acquire_Release(t *testing.T) {
	th := newThrottle(2)

	require.NoError(t, th.Acquire(context.Background()))
	require.NoError(t, th.Acquire(context.Background()))

	done := make(chan struct{})
	go func() {
		require.NoError(t, th.Acquire(context.Background()))
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("acquire should block when limit reached")
	case <-time.After(50 * time.Millisecond):
	}

	th.Release()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("acquire should unblock after release")
	}
}

func TestThrottle_ContextCancel(t *testing.T) {
	th := newThrottle(1)
	require.NoError(t, th.Acquire(context.Background()))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := th.Acquire(ctx)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestThrottle_ConcurrentLimit(t *testing.T) {
	const limit = 3
	th := newThrottle(limit)

	var active atomic.Int32
	var maxActive atomic.Int32
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			require.NoError(t, th.Acquire(context.Background()))
			defer th.Release()

			cur := active.Add(1)
			for {
				old := maxActive.Load()
				if cur <= old || maxActive.CompareAndSwap(old, cur) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			active.Add(-1)
		}()
	}

	wg.Wait()
	assert.LessOrEqual(t, maxActive.Load(), int32(limit))
}
