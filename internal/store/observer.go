package store

import "time"

// Observer lets code outside dbstore observe Directory/Executor internals —
// source lifecycle and Executor.Run's throttle wait, in-flight state, and
// outcome — for metrics, tracing, or logging. It is optional and
// vendor-neutral for the same reason PoolConfigApplier/Closer are: nothing
// here assumes Prometheus or any other backend. adapters/prometheus is a
// ready-made implementation built on these hooks, not a special case of them.
//
// All methods are called synchronously, inline in the goroutine driving
// Register/Remove/Run/SetObserver — the same constraint
// net/http/httptrace.ClientTrace's hooks document. An implementation must
// not block, do I/O, or acquire a lock another goroutine might hold while
// calling back into dbstore, or it distorts the very durations/timings it's
// there to measure (and, in the Run case, holds the throttle slot or
// in-flight count open longer than the operation itself did).
//
// Unlike httptrace, a panicking Observer method does not crash the call that
// triggered it: dbstore recovers around every Observer invocation (see
// safeObserve) and discards the panic. Observability is deliberately
// best-effort relative to the actual operation — a bug in a metrics or
// logging Observer must never be able to fail a repository call whose fn
// itself succeeded.
type Observer interface {
	// ObserveSourceSnapshot is called once, synchronously, by SetObserver,
	// with every source currently registered at the moment this Observer is
	// attached. It exists to bring a newly (or re-)attached Observer's view
	// up to date without pretending those sources were just registered —
	// it is a state sync, not a lifecycle event. An implementation tracking
	// a live count (e.g. a gauge) should set it from len(sources) directly
	// rather than incrementing per element, and must not treat this as
	// contributing to a registration-events counter: calling SetObserver
	// twice, or with different Observer values over a process's lifetime,
	// must not inflate that count. sources is nil, not called with an empty
	// non-nil slice, when nothing is registered yet.
	ObserveSourceSnapshot(sources []string)
	// ObserveSourceRegistered is called when Directory.Register successfully
	// opens and registers a new source — a genuine lifecycle event, exactly
	// once per Register call that succeeds. Never called for sources an
	// Observer already knows about via ObserveSourceSnapshot.
	ObserveSourceRegistered(source string)
	// ObserveSourceRemoved is called when Directory.Remove or RemoveAll
	// takes a source out of the registry — a genuine lifecycle event,
	// exactly once per source actually removed. It fires as soon as the
	// source is no longer registered (so Executor.Run can no longer find
	// it), which is before Remove/RemoveAll wait for that source's
	// in-flight operations to finish and close its client: "removed" means
	// "no longer in the registry", not "fully drained and closed".
	ObserveSourceRemoved(source string)
	// ObserveAcquire is called once per Run call, right after the throttle
	// either grants access (err is nil) or Run gives up because ctx was
	// cancelled while waiting (err is ctx.Err(), and fn never runs).
	ObserveAcquire(source string, waited time.Duration, err error)
	// ObserveComplete is called after fn returns, once per Run call that
	// got past ObserveAcquire successfully — pairing with it is what lets an
	// Observer track in-flight operations (e.g. Inc on ObserveAcquire's
	// success, Dec here). duration covers only fn's execution.
	ObserveComplete(source string, duration time.Duration, err error)
}

// safeObserve calls fn — always a single Observer method invocation — and
// recovers if it panics, per Observer's doc comment: an Observer bug must
// never crash the Register/Remove/SetObserver/Run call that triggered it.
// The panic is discarded, not logged, because core has no logging facility
// to discard it into safely either; an Observer that needs its own panics
// visible should recover and report them itself.
func safeObserve(fn func()) {
	defer func() { _ = recover() }()
	fn()
}

// MultiObserver fans a single Observer call out to every Observer in the
// slice, in order — the Observer equivalent of io.MultiWriter, for
// attaching more than one (e.g. Prometheus metrics and a custom audit log)
// with SetObserver, which only holds one. Each member is called through
// safeObserve individually, so one member panicking doesn't stop the rest
// of the group from being notified.
type MultiObserver []Observer

func (m MultiObserver) ObserveSourceSnapshot(sources []string) {
	for _, o := range m {
		safeObserve(func() { o.ObserveSourceSnapshot(sources) })
	}
}

func (m MultiObserver) ObserveSourceRegistered(source string) {
	for _, o := range m {
		safeObserve(func() { o.ObserveSourceRegistered(source) })
	}
}

func (m MultiObserver) ObserveSourceRemoved(source string) {
	for _, o := range m {
		safeObserve(func() { o.ObserveSourceRemoved(source) })
	}
}

func (m MultiObserver) ObserveAcquire(source string, waited time.Duration, err error) {
	for _, o := range m {
		safeObserve(func() { o.ObserveAcquire(source, waited, err) })
	}
}

func (m MultiObserver) ObserveComplete(source string, duration time.Duration, err error) {
	for _, o := range m {
		safeObserve(func() { o.ObserveComplete(source, duration, err) })
	}
}
