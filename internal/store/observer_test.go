package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordedAcquire struct {
	source string
	waited time.Duration
	err    error
}

type recordedComplete struct {
	source   string
	duration time.Duration
	err      error
}

type fakeObserver struct {
	snapshots  [][]string
	registered []string
	removed    []string
	acquires   []recordedAcquire
	completes  []recordedComplete
}

func (o *fakeObserver) ObserveSourceSnapshot(sources []string) {
	o.snapshots = append(o.snapshots, sources)
}

func (o *fakeObserver) ObserveSourceRegistered(source string) {
	o.registered = append(o.registered, source)
}
func (o *fakeObserver) ObserveSourceRemoved(source string) { o.removed = append(o.removed, source) }

func (o *fakeObserver) ObserveAcquire(source string, waited time.Duration, err error) {
	o.acquires = append(o.acquires, recordedAcquire{source: source, waited: waited, err: err})
}

func (o *fakeObserver) ObserveComplete(source string, duration time.Duration, err error) {
	o.completes = append(o.completes, recordedComplete{source: source, duration: duration, err: err})
}

// orderTrackingObserver is used to force and detect the exact race from
// review: SetObserver's data capture (mu) can linearize before Register's,
// while SetObserver's callback is artificially held up, to check whether
// Register's callback can slip in ahead of it once observerMu blocks it.
type orderTrackingObserver struct {
	mu     sync.Mutex
	events []string

	snapshotStarted chan struct{}
	releaseSnapshot chan struct{}
}

func (o *orderTrackingObserver) ObserveSourceSnapshot(sources []string) {
	close(o.snapshotStarted)
	<-o.releaseSnapshot
	o.mu.Lock()
	o.events = append(o.events, "snapshot")
	o.mu.Unlock()
}

func (o *orderTrackingObserver) ObserveSourceRegistered(source string) {
	o.mu.Lock()
	o.events = append(o.events, "registered:"+source)
	o.mu.Unlock()
}
func (o *orderTrackingObserver) ObserveSourceRemoved(string)                  {}
func (o *orderTrackingObserver) ObserveAcquire(string, time.Duration, error)  {}
func (o *orderTrackingObserver) ObserveComplete(string, time.Duration, error) {}

type panicOnRegisterObserver struct{}

func (panicOnRegisterObserver) ObserveSourceSnapshot(sources []string)       {}
func (panicOnRegisterObserver) ObserveSourceRegistered(source string)        { panic("boom") }
func (panicOnRegisterObserver) ObserveSourceRemoved(string)                  {}
func (panicOnRegisterObserver) ObserveAcquire(string, time.Duration, error)  {}
func (panicOnRegisterObserver) ObserveComplete(string, time.Duration, error) {}

type panicOnAcquireObserver struct{}

func (panicOnAcquireObserver) ObserveSourceSnapshot([]string) {}
func (panicOnAcquireObserver) ObserveSourceRegistered(string) {}
func (panicOnAcquireObserver) ObserveSourceRemoved(string)    {}
func (panicOnAcquireObserver) ObserveAcquire(string, time.Duration, error) {
	panic("acquire-boom")
}
func (panicOnAcquireObserver) ObserveComplete(string, time.Duration, error) {}

type panicOnCompleteObserver struct{}

func (panicOnCompleteObserver) ObserveSourceSnapshot([]string)              {}
func (panicOnCompleteObserver) ObserveSourceRegistered(string)              {}
func (panicOnCompleteObserver) ObserveSourceRemoved(string)                 {}
func (panicOnCompleteObserver) ObserveAcquire(string, time.Duration, error) {}
func (panicOnCompleteObserver) ObserveComplete(string, time.Duration, error) {
	panic("complete-boom")
}

func TestDirectory_NotifiesObserverOnRegisterAndRemove(t *testing.T) {
	obs := &fakeObserver{}
	pool := newTestDirectory()
	pool.SetObserver(obs)
	defer pool.RemoveAll()

	require.NoError(t, pool.Register("primary", testConfig(":memory:")))
	require.Equal(t, []string{"primary"}, obs.registered)

	require.NoError(t, pool.Remove("primary"))
	require.Equal(t, []string{"primary"}, obs.removed)
}

func TestDirectory_RemoveAll_NotifiesObserverPerSource(t *testing.T) {
	obs := &fakeObserver{}
	pool := newTestDirectory()
	pool.SetObserver(obs)

	require.NoError(t, pool.Register("a", testConfig(":memory:")))
	require.NoError(t, pool.Register("b", testConfig(":memory:")))

	pool.RemoveAll()

	assert.ElementsMatch(t, []string{"a", "b"}, obs.removed)
}

// TestDirectory_SetObserver_SnapshotsSourcesRegisteredBeforeIt is the
// regression test for the "SetObserver called after Open" bug: without the
// snapshot, an Observer attached later would never learn "primary" exists,
// but would still see its eventual ObserveSourceRemoved — e.g. a Prometheus
// sources_active gauge going negative.
func TestDirectory_SetObserver_SnapshotsSourcesRegisteredBeforeIt(t *testing.T) {
	pool := newTestDirectory()
	defer pool.RemoveAll()

	// Register before any Observer is attached at all.
	require.NoError(t, pool.Register("primary", testConfig(":memory:")))

	obs := &fakeObserver{}
	pool.SetObserver(obs)

	require.Equal(t, [][]string{{"primary"}}, obs.snapshots,
		"SetObserver must snapshot already-registered sources, not just apply to future ones")
	assert.Empty(t, obs.registered,
		"a snapshot must not also fire as an ObserveSourceRegistered event")

	require.NoError(t, pool.Remove("primary"))
	require.Equal(t, []string{"primary"}, obs.removed)
}

func TestDirectory_SetObserver_SecondObserverAlsoGetsSnapshot(t *testing.T) {
	pool := newTestDirectory()
	defer pool.RemoveAll()

	require.NoError(t, pool.Register("a", testConfig(":memory:")))
	require.NoError(t, pool.Register("b", testConfig(":memory:")))

	first := &fakeObserver{}
	pool.SetObserver(first)
	require.Len(t, first.snapshots, 1)
	assert.ElementsMatch(t, []string{"a", "b"}, first.snapshots[0])

	// Swapping to a second Observer must snapshot to it too — it has never
	// seen "a" or "b" before, regardless of what the first Observer saw.
	second := &fakeObserver{}
	pool.SetObserver(second)
	require.Len(t, second.snapshots, 1)
	assert.ElementsMatch(t, []string{"a", "b"}, second.snapshots[0])
}

// TestDirectory_SetObserver_CalledTwiceDoesNotDoubleCountRegistrations is the
// regression test for calling SetObserver more than once (the same Observer,
// or reattaching): each call must produce its own snapshot rather than
// replaying ObserveSourceRegistered again, which would double-count any
// Observer (like adapters/prometheus's) that increments a gauge or counter
// per registration event.
func TestDirectory_SetObserver_CalledTwiceDoesNotDoubleCountRegistrations(t *testing.T) {
	pool := newTestDirectory()
	defer pool.RemoveAll()

	require.NoError(t, pool.Register("primary", testConfig(":memory:")))

	obs := &fakeObserver{}
	pool.SetObserver(obs)
	pool.SetObserver(obs) // same Observer, called again

	require.Len(t, obs.snapshots, 2, "each SetObserver call produces its own snapshot")
	assert.Empty(t, obs.registered, "neither call should have fired a registration event")

	require.NoError(t, pool.Remove("primary"))
	require.Equal(t, []string{"primary"}, obs.removed, "exactly one removal event for one actual Remove")
}

func TestDirectory_SetObserver_EmptyDirectorySnapshotsNil(t *testing.T) {
	pool := newTestDirectory()
	defer pool.RemoveAll()

	obs := &fakeObserver{}
	pool.SetObserver(obs)

	require.Equal(t, [][]string{nil}, obs.snapshots)
}

// TestDirectory_ObserverCallbacksOrderedWithDataMutations is the regression
// test for review's second finding: data captured under mu was correctly
// ordered, but the callbacks delivering that data were not, since nothing
// serialized them once mu was released. This forces the exact bad
// interleaving — SetObserver linearizes first (on an empty Directory) but
// its callback is held up — and checks that a concurrent Register can't
// deliver its own callback first just because the earlier one is slow.
func TestDirectory_ObserverCallbacksOrderedWithDataMutations(t *testing.T) {
	pool := newTestDirectory()
	defer pool.RemoveAll()

	obs := &orderTrackingObserver{
		snapshotStarted: make(chan struct{}),
		releaseSnapshot: make(chan struct{}),
	}

	setObserverDone := make(chan struct{})
	go func() {
		pool.SetObserver(obs) // Directory is empty: snapshot(nil), and it blocks mid-callback
		close(setObserverDone)
	}()

	<-obs.snapshotStarted // SetObserver's data capture has linearized; its callback is now blocked

	registerDone := make(chan struct{})
	go func() {
		require.NoError(t, pool.Register("primary", testConfig(":memory:")))
		close(registerDone)
	}()

	// Register's data mutation can proceed (mu was released), but its
	// callback needs observerMu, which SetObserver's still-blocked callback
	// holds — so Register must not be able to finish yet.
	select {
	case <-registerDone:
		t.Fatal("Register finished before the earlier SetObserver's callback did — callback ordering not enforced")
	case <-time.After(50 * time.Millisecond):
	}

	close(obs.releaseSnapshot) // let SetObserver's callback complete and release observerMu

	<-setObserverDone
	<-registerDone

	obs.mu.Lock()
	defer obs.mu.Unlock()
	require.Equal(t, []string{"snapshot", "registered:primary"}, obs.events,
		"the snapshot that linearized first must also be delivered first")
}

// TestDirectory_ObserverPanicDoesNotCrashOrDeadlockRegister covers two
// review findings together: (1) an Observer callback used to leave
// observerMu locked forever if it panicked, hanging every future
// SetObserver/Register/Remove/RemoveAll call; (2) even after that was fixed
// with a bare defer, the panic itself still propagated out of Register,
// which is its own bug — a bug in a metrics/logging Observer must not make
// a successful Register call look like it failed. safeObserve now recovers
// the panic, so Register returns normally (the source really is registered)
// and observerMu isn't left locked.
func TestDirectory_ObserverPanicDoesNotCrashOrDeadlockRegister(t *testing.T) {
	pool := newTestDirectory()
	defer pool.RemoveAll()

	pool.SetObserver(panicOnRegisterObserver{})

	require.NotPanics(t, func() {
		require.NoError(t, pool.Register("primary", testConfig(":memory:")))
	})

	// The source is really registered despite the Observer panicking.
	executor := NewExecutor(pool)
	require.NoError(t, executor.Run(context.Background(), "primary", func(ctx context.Context, db *sqlx.DB) error {
		return nil
	}))

	// If observerMu leaked locked, this would hang forever instead of
	// returning — bounded by a timeout so a regression fails the test
	// instead of hanging the whole suite.
	done := make(chan struct{})
	go func() {
		pool.SetObserver(&fakeObserver{})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SetObserver hung after an earlier Observer callback panicked — observerMu leaked locked")
	}
}

// reentrantIntoRegisterObserver calls back into Register on the same
// Directory from inside ObserveSourceRegistered — the self-deadlock
// observerCallbackGuard exists to catch. It recovers that panic itself (rather than
// letting it reach the outer safeObserve) so the test can inspect it.
type reentrantIntoRegisterObserver struct {
	dir       *Directory[*sqlx.DB]
	recovered any
}

func (r *reentrantIntoRegisterObserver) ObserveSourceSnapshot([]string) {}
func (r *reentrantIntoRegisterObserver) ObserveSourceRegistered(source string) {
	if source != "first" {
		return
	}
	defer func() { r.recovered = recover() }()
	_ = r.dir.Register("second", testConfig(":memory:"))
}
func (r *reentrantIntoRegisterObserver) ObserveSourceRemoved(string)                  {}
func (r *reentrantIntoRegisterObserver) ObserveAcquire(string, time.Duration, error)  {}
func (r *reentrantIntoRegisterObserver) ObserveComplete(string, time.Duration, error) {}

func TestDirectory_ObserverReentrancyIntoRegisterPanicsInsteadOfHanging(t *testing.T) {
	pool := newTestDirectory()
	defer pool.RemoveAll()

	obs := &reentrantIntoRegisterObserver{dir: pool}
	pool.SetObserver(obs)

	done := make(chan struct{})
	var registerErr error
	go func() {
		defer close(done)
		registerErr = pool.Register("first", testConfig(":memory:"))
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("DEADLOCK: Observer callback reentering Register hung instead of panicking")
	}

	// The reentrant nested Register("second") call panicked...
	require.NotNil(t, obs.recovered, "expected the reentrant nested Register call to panic")
	require.Contains(t, fmt.Sprint(obs.recovered), "called back into")

	// ...but the outer Register("first") call that triggered it still
	// succeeds, per Observer's documented panic-safety contract.
	require.NoError(t, registerErr)
	_, err := pool.get("second")
	require.Error(t, err, "a rejected reentrant Register must not publish its source")

	// Neither mu nor observerMu leaked locked: the Directory is still fully
	// usable afterward.
	executor := NewExecutor(pool)
	require.NoError(t, executor.Run(context.Background(), "first", func(ctx context.Context, db *sqlx.DB) error {
		return nil
	}))
	require.NoError(t, pool.Remove("first"))
}

// reentrantIntoSetObserverObserver calls back into SetObserver on the same
// Directory from inside ObserveSourceSnapshot.
type reentrantIntoSetObserverObserver struct {
	dir       *Directory[*sqlx.DB]
	recovered any
}

func (r *reentrantIntoSetObserverObserver) ObserveSourceSnapshot([]string) {
	defer func() { r.recovered = recover() }()
	r.dir.SetObserver(r)
}
func (r *reentrantIntoSetObserverObserver) ObserveSourceRegistered(string)               {}
func (r *reentrantIntoSetObserverObserver) ObserveSourceRemoved(string)                  {}
func (r *reentrantIntoSetObserverObserver) ObserveAcquire(string, time.Duration, error)  {}
func (r *reentrantIntoSetObserverObserver) ObserveComplete(string, time.Duration, error) {}

func TestDirectory_ObserverReentrancyIntoSetObserverPanicsInsteadOfHanging(t *testing.T) {
	pool := newTestDirectory()
	defer pool.RemoveAll()

	obs := &reentrantIntoSetObserverObserver{dir: pool}

	done := make(chan struct{})
	go func() {
		defer close(done)
		pool.SetObserver(obs)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("DEADLOCK: Observer callback reentering SetObserver hung instead of panicking")
	}

	require.NotNil(t, obs.recovered, "expected the reentrant nested SetObserver call to panic")
	require.Contains(t, fmt.Sprint(obs.recovered), "called back into")
	require.Same(t, obs, pool.getObserver(), "a rejected reentrant SetObserver must not replace the observer")

	// The Directory is still fully usable afterward.
	require.NoError(t, pool.Register("primary", testConfig(":memory:")))
}

// reentrantRemoveObserver verifies that lifecycle reentry is rejected before
// Remove deletes the entry. Before observerCallbackGuard, the entry was removed
// first and the late observerLock panic skipped both draining and closing it.
type reentrantRemoveObserver struct {
	dir       *Directory[*sqlx.DB]
	recovered any
}

func (r *reentrantRemoveObserver) ObserveSourceSnapshot([]string) {}
func (r *reentrantRemoveObserver) ObserveSourceRegistered(source string) {
	defer func() { r.recovered = recover() }()
	_ = r.dir.Remove(source)
}
func (r *reentrantRemoveObserver) ObserveSourceRemoved(string)                  {}
func (r *reentrantRemoveObserver) ObserveAcquire(string, time.Duration, error)  {}
func (r *reentrantRemoveObserver) ObserveComplete(string, time.Duration, error) {}

func TestDirectory_ObserverReentrancyIntoRemoveDoesNotDeleteSource(t *testing.T) {
	pool := newTestDirectory()
	defer pool.RemoveAll()

	obs := &reentrantRemoveObserver{dir: pool}
	pool.SetObserver(obs)
	require.NoError(t, pool.Register("primary", testConfig(":memory:")))

	require.NotNil(t, obs.recovered)
	require.Contains(t, fmt.Sprint(obs.recovered), "called back into")
	_, err := pool.get("primary")
	require.NoError(t, err, "a rejected reentrant Remove must leave the source registered")
}

// reentrantRunObserver covers Run callbacks, which do not hold observerMu.
// A Remove from ObserveAcquire/ObserveComplete used to wait for the very Run
// executing the callback and deadlock without reaching observerLock.
type reentrantRunObserver struct {
	dir       *Directory[*sqlx.DB]
	phase     string
	recovered any
}

func (r *reentrantRunObserver) ObserveSourceSnapshot([]string) {}
func (r *reentrantRunObserver) ObserveSourceRegistered(string) {}
func (r *reentrantRunObserver) ObserveSourceRemoved(string)    {}
func (r *reentrantRunObserver) ObserveAcquire(source string, _ time.Duration, err error) {
	if r.phase != "acquire" || err != nil {
		return
	}
	defer func() { r.recovered = recover() }()
	_ = r.dir.Remove(source)
}
func (r *reentrantRunObserver) ObserveComplete(source string, _ time.Duration, _ error) {
	if r.phase != "complete" {
		return
	}
	defer func() { r.recovered = recover() }()
	_ = r.dir.Remove(source)
}

func TestExecutor_ObserverRunCallbackReentrancyPanicsInsteadOfHanging(t *testing.T) {
	for _, phase := range []string{"acquire", "complete"} {
		t.Run(phase, func(t *testing.T) {
			pool := newTestDirectory()
			defer pool.RemoveAll()
			require.NoError(t, pool.Register("primary", testConfig(":memory:")))

			obs := &reentrantRunObserver{dir: pool, phase: phase}
			pool.SetObserver(obs)
			done := make(chan error, 1)
			go func() {
				done <- NewExecutor(pool).Run(context.Background(), "primary", func(context.Context, *sqlx.DB) error {
					return nil
				})
			}()

			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(2 * time.Second):
				t.Fatal("DEADLOCK: Run Observer callback reentering Remove hung")
			}

			require.NotNil(t, obs.recovered)
			require.Contains(t, fmt.Sprint(obs.recovered), "called back into")
			_, err := pool.get("primary")
			require.NoError(t, err, "a rejected reentrant Remove must leave the source registered")
		})
	}
}

// TestDirectory_ConcurrentRegisterWithObserverNeverFalsePositives proves the
// reentrancy guard only fires on genuine same-goroutine reentrancy: many
// unrelated goroutines legitimately contending for observerMu (via Register)
// at once must still just block on each other like a plain mutex, never
// panic and never hang.
func TestDirectory_ConcurrentRegisterWithObserverNeverFalsePositives(t *testing.T) {
	pool := newTestDirectory()
	defer pool.RemoveAll()
	pool.SetObserver(&fakeObserver{})

	const n = 50
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = pool.Register(fmt.Sprintf("source-%d", i), testConfig(":memory:"))
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "source-%d", i)
	}
}

func TestExecutor_Run_SurvivesPanickingObserveAcquire(t *testing.T) {
	pool := newTestDirectory()
	defer pool.RemoveAll()
	require.NoError(t, pool.Register("primary", testConfig(":memory:")))
	pool.SetObserver(panicOnAcquireObserver{})

	executor := NewExecutor(pool)
	var called bool
	err := executor.Run(context.Background(), "primary", func(ctx context.Context, db *sqlx.DB) error {
		called = true
		return nil
	})
	require.NoError(t, err)
	assert.True(t, called, "fn must still run even though ObserveAcquire panicked")
}

func TestExecutor_Run_SurvivesPanickingObserveComplete(t *testing.T) {
	pool := newTestDirectory()
	defer pool.RemoveAll()
	require.NoError(t, pool.Register("primary", testConfig(":memory:")))
	pool.SetObserver(panicOnCompleteObserver{})

	executor := NewExecutor(pool)
	err := executor.Run(context.Background(), "primary", func(ctx context.Context, db *sqlx.DB) error {
		return nil
	})
	require.NoError(t, err, "a panicking ObserveComplete must not fail a Run whose fn succeeded")
}

// TestExecutor_Run_FnPanicStillPropagatesEvenIfObserveCompleteAlsoPanics
// proves the two recover layers compose correctly: fn's panic must reach
// the caller unchanged even when the Observer notified about it panics too
// — the Observer's panic must not replace or swallow fn's.
func TestExecutor_Run_FnPanicStillPropagatesEvenIfObserveCompleteAlsoPanics(t *testing.T) {
	pool := newTestDirectory()
	defer pool.RemoveAll()
	require.NoError(t, pool.Register("primary", testConfig(":memory:")))
	pool.SetObserver(panicOnCompleteObserver{})

	executor := NewExecutor(pool)
	require.PanicsWithValue(t, "fn-boom", func() {
		_ = executor.Run(context.Background(), "primary", func(ctx context.Context, db *sqlx.DB) error {
			panic("fn-boom")
		})
	})
}

func TestExecutor_Run_NotifiesObserverOnSuccess(t *testing.T) {
	pool := newTestDirectory()
	defer pool.RemoveAll()
	require.NoError(t, pool.Register("primary", testConfig(":memory:")))

	obs := &fakeObserver{}
	pool.SetObserver(obs)

	executor := NewExecutor(pool)
	require.NoError(t, executor.Run(context.Background(), "primary", func(ctx context.Context, db *sqlx.DB) error {
		time.Sleep(5 * time.Millisecond)
		return nil
	}))

	require.Len(t, obs.acquires, 1)
	assert.Equal(t, "primary", obs.acquires[0].source)
	assert.NoError(t, obs.acquires[0].err)

	require.Len(t, obs.completes, 1)
	assert.Equal(t, "primary", obs.completes[0].source)
	assert.NoError(t, obs.completes[0].err)
	assert.GreaterOrEqual(t, obs.completes[0].duration, 5*time.Millisecond)
}

func TestExecutor_Run_NotifiesObserverOnError(t *testing.T) {
	pool := newTestDirectory()
	defer pool.RemoveAll()
	require.NoError(t, pool.Register("primary", testConfig(":memory:")))

	obs := &fakeObserver{}
	pool.SetObserver(obs)

	executor := NewExecutor(pool)
	wantErr := errors.New("boom")
	err := executor.Run(context.Background(), "primary", func(ctx context.Context, db *sqlx.DB) error {
		return wantErr
	})
	require.ErrorIs(t, err, wantErr)

	require.Len(t, obs.acquires, 1)
	assert.NoError(t, obs.acquires[0].err, "acquire itself succeeded — the error is fn's")

	require.Len(t, obs.completes, 1)
	assert.ErrorIs(t, obs.completes[0].err, wantErr)
}

func TestExecutor_Run_NotifiesObserverOnThrottleTimeout(t *testing.T) {
	pool := newTestDirectory()
	defer pool.RemoveAll()

	cfg := testConfig(":memory:")
	cfg.PoolConfig.MaxConcurrency = 1
	require.NoError(t, pool.Register("primary", cfg))

	obs := &fakeObserver{}
	pool.SetObserver(obs)

	executor := NewExecutor(pool)

	blocked := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = executor.Run(context.Background(), "primary", func(ctx context.Context, db *sqlx.DB) error {
			close(blocked)
			<-release
			return nil
		})
	}()
	<-blocked
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := executor.Run(ctx, "primary", func(ctx context.Context, db *sqlx.DB) error {
		return nil
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)

	// The second Run's acquire failed, so it must be recorded on acquires
	// but never reach completes (fn never ran for that call).
	var timedOut int
	for _, a := range obs.acquires {
		if errors.Is(a.err, context.DeadlineExceeded) {
			timedOut++
		}
	}
	assert.Equal(t, 1, timedOut)
	// The first Run is still blocked on <-release at this point (it only
	// unblocks via the deferred close(release) once this test function
	// returns), so it can't have reached ObserveComplete yet.
	assert.Len(t, obs.completes, 0, "the still-blocked first Run must not have completed yet")
}

func TestExecutor_Run_ObserveCompleteFiresOnPanicAndPanicStillPropagates(t *testing.T) {
	pool := newTestDirectory()
	defer pool.RemoveAll()
	require.NoError(t, pool.Register("primary", testConfig(":memory:")))

	obs := &fakeObserver{}
	pool.SetObserver(obs)

	executor := NewExecutor(pool)

	require.Panics(t, func() {
		_ = executor.Run(context.Background(), "primary", func(ctx context.Context, db *sqlx.DB) error {
			panic("boom")
		})
	})

	// If this weren't deferred, a panic in fn would skip ObserveComplete
	// entirely, leaking whatever gauge (e.g. in-flight) an Observer paired
	// with ObserveAcquire.
	require.Len(t, obs.completes, 1)
	assert.Error(t, obs.completes[0].err)
	assert.Contains(t, obs.completes[0].err.Error(), "boom")
}

func TestExecutor_Run_NoObserverIsFine(t *testing.T) {
	pool := newTestDirectory()
	defer pool.RemoveAll()
	require.NoError(t, pool.Register("primary", testConfig(":memory:")))

	executor := NewExecutor(pool)
	err := executor.Run(context.Background(), "primary", func(ctx context.Context, db *sqlx.DB) error {
		return nil
	})
	assert.NoError(t, err)
}

func TestMultiObserver_FansOutToEveryMember(t *testing.T) {
	first := &fakeObserver{}
	second := &fakeObserver{}
	multi := MultiObserver{first, second}

	pool := newTestDirectory()
	pool.SetObserver(multi)
	defer pool.RemoveAll()

	require.NoError(t, pool.Register("primary", testConfig(":memory:")))

	executor := NewExecutor(pool)
	require.NoError(t, executor.Run(context.Background(), "primary", func(ctx context.Context, db *sqlx.DB) error {
		return nil
	}))

	require.NoError(t, pool.Remove("primary"))

	for _, obs := range []*fakeObserver{first, second} {
		assert.Equal(t, []string{"primary"}, obs.registered)
		assert.Equal(t, []string{"primary"}, obs.removed)
		assert.Len(t, obs.acquires, 1)
		assert.Len(t, obs.completes, 1)
	}
}
