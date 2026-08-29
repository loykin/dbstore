package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type observerEvent struct {
	kind   string
	source string
	err    error
}

type recordingObserver struct {
	mu     sync.Mutex
	events []observerEvent
}

func (o *recordingObserver) record(event observerEvent) {
	o.mu.Lock()
	o.events = append(o.events, event)
	o.mu.Unlock()
}

func (o *recordingObserver) ObserveSourceRegistered(source string) {
	o.record(observerEvent{kind: "registered", source: source})
}
func (o *recordingObserver) ObserveSourceRemoved(source string) {
	o.record(observerEvent{kind: "removed", source: source})
}
func (o *recordingObserver) ObserveAcquire(source string, _ time.Duration, err error) {
	o.record(observerEvent{kind: "acquire", source: source, err: err})
}
func (o *recordingObserver) ObserveComplete(source string, _ time.Duration, err error) {
	o.record(observerEvent{kind: "complete", source: source, err: err})
}
func (o *recordingObserver) snapshot() []observerEvent {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]observerEvent(nil), o.events...)
}

func TestAdapter_WithObserverCoversFirstSourceAndRun(t *testing.T) {
	observer := &recordingObserver{}
	adapter := NewAdapter[int](WithObserver(observer))
	adapter.RegisterDriver("driver", intDriver(1))
	defer adapter.Close()

	require.NoError(t, adapter.Open("primary", SourceConfig{Driver: "driver"}))
	require.NoError(t, adapter.Executor().Run(context.Background(), "primary", func(context.Context, int) error { return nil }))

	events := observer.snapshot()
	require.Len(t, events, 3)
	require.Equal(t, "registered", events[0].kind)
	require.Equal(t, "acquire", events[1].kind)
	require.Equal(t, "complete", events[2].kind)
}

func TestAdapter_WithObserverRejectsDuplicateConfiguration(t *testing.T) {
	require.PanicsWithValue(t, "dbstore: Observer configured more than once", func() {
		NewAdapter[int](WithObserver(&recordingObserver{}), WithObserver(&recordingObserver{}))
	})
}

type orderedObserver struct {
	registeredStarted chan struct{}
	releaseRegistered chan struct{}
	mu                sync.Mutex
	events            []string
}

func (o *orderedObserver) ObserveSourceRegistered(source string) {
	close(o.registeredStarted)
	<-o.releaseRegistered
	o.mu.Lock()
	o.events = append(o.events, "registered:"+source)
	o.mu.Unlock()
}
func (o *orderedObserver) ObserveSourceRemoved(source string) {
	o.mu.Lock()
	o.events = append(o.events, "removed:"+source)
	o.mu.Unlock()
}
func (*orderedObserver) ObserveAcquire(string, time.Duration, error)  {}
func (*orderedObserver) ObserveComplete(string, time.Duration, error) {}

func TestDirectory_LifecycleCallbacksFollowMutationOrder(t *testing.T) {
	observer := &orderedObserver{registeredStarted: make(chan struct{}), releaseRegistered: make(chan struct{})}
	registry := NewDriverRegistry[int]()
	registry.Register("driver", intDriver(1))
	directory := NewDirectory(registry, observer)

	registered := make(chan error, 1)
	go func() { registered <- directory.Register("primary", SourceConfig{Driver: "driver"}) }()
	<-observer.registeredStarted

	removed := make(chan error, 1)
	go func() { removed <- directory.Remove("primary") }()
	select {
	case err := <-removed:
		t.Fatalf("Remove returned before registration callback: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	close(observer.releaseRegistered)
	require.NoError(t, <-registered)
	require.NoError(t, <-removed)
	require.Equal(t, []string{"registered:primary", "removed:primary"}, observer.events)
	directory.RemoveAll()
}

type panickingObserver struct{ panicOn string }

func (o panickingObserver) maybePanic(method string) {
	if o.panicOn == method {
		panic("observer failure")
	}
}
func (o panickingObserver) ObserveSourceRegistered(string) { o.maybePanic("registered") }
func (o panickingObserver) ObserveSourceRemoved(string)    { o.maybePanic("removed") }
func (o panickingObserver) ObserveAcquire(string, time.Duration, error) {
	o.maybePanic("acquire")
}
func (o panickingObserver) ObserveComplete(string, time.Duration, error) {
	o.maybePanic("complete")
}

func TestDirectory_ObserverPanicsNeverFailOperations(t *testing.T) {
	for _, method := range []string{"registered", "removed", "acquire", "complete"} {
		t.Run(method, func(t *testing.T) {
			registry := NewDriverRegistry[int]()
			registry.Register("driver", intDriver(1))
			directory := NewDirectory(registry, panickingObserver{panicOn: method})

			require.NoError(t, directory.Register("primary", SourceConfig{Driver: "driver"}))
			require.NoError(t, NewExecutor(directory).Run(context.Background(), "primary", func(context.Context, int) error { return nil }))
			require.NoError(t, directory.Remove("primary"))
			directory.RemoveAll()
		})
	}
}

func TestExecutor_ObserverReportsCanceledAcquireAndRunError(t *testing.T) {
	observer := &recordingObserver{}
	registry := NewDriverRegistry[int]()
	registry.Register("driver", intDriver(1))
	directory := NewDirectory(registry, observer)
	defer directory.RemoveAll()
	require.NoError(t, directory.Register("primary", SourceConfig{Driver: "driver", PoolConfig: PoolConfig{MaxConcurrency: 1}}))

	exec := NewExecutor(directory)
	entered := make(chan struct{})
	release := make(chan struct{})
	first := make(chan error, 1)
	go func() {
		first <- exec.Run(context.Background(), "primary", func(context.Context, int) error {
			close(entered)
			<-release
			return errors.New("run failed")
		})
	}()
	<-entered

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, exec.Run(ctx, "primary", func(context.Context, int) error {
		t.Fatal("canceled callback ran")
		return nil
	}), context.Canceled)
	close(release)
	require.EqualError(t, <-first, "run failed")

	var canceledAcquire, failedComplete bool
	for _, event := range observer.snapshot() {
		canceledAcquire = canceledAcquire || (event.kind == "acquire" && errors.Is(event.err, context.Canceled))
		failedComplete = failedComplete || (event.kind == "complete" && event.err != nil && event.err.Error() == "run failed")
	}
	require.True(t, canceledAcquire)
	require.True(t, failedComplete)
}

func TestMultiObserver_IsolatesMembers(t *testing.T) {
	good := &recordingObserver{}
	multi := MultiObserver{panickingObserver{panicOn: "registered"}, good}
	multi.ObserveSourceRegistered("primary")
	require.Equal(t, []observerEvent{{kind: "registered", source: "primary"}}, good.snapshot())
}
