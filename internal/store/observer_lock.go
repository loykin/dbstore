package store

import (
	"bytes"
	"fmt"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

// reentrantObserverPanic is the message every self-reentry panic uses, kept
// as a constant so tests can match on it without duplicating the wording.
const reentrantObserverPanic = "dbstore: an Observer callback called back into " +
	"Register/Remove/RemoveAll/SetObserver on the same Directory it was " +
	"triggered from — this can self-deadlock or partially mutate lifecycle " +
	"state, so dbstore panics before changing that state. Do the reentrant work " +
	"from a separate goroutine (e.g. send it over a channel) instead of " +
	"calling back into the Directory synchronously from inside the callback."

// observerCallbackGuard records which goroutines are currently executing an
// Observer callback for one Directory. Lifecycle methods consult it before
// touching state, so reentry is rejected before Register inserts an entry,
// Remove deletes one, or RemoveAll swaps the map. It also covers Run callbacks,
// which do not hold observerMu but can otherwise self-deadlock by calling Remove
// while their own in-flight count is still active.
type observerCallbackGuard struct {
	mu     sync.Mutex
	active map[int64]int
}

func (g *observerCallbackGuard) enter() func() {
	gid := currentGoroutineID()
	g.mu.Lock()
	if g.active == nil {
		g.active = make(map[int64]int)
	}
	g.active[gid]++
	g.mu.Unlock()

	return func() {
		g.mu.Lock()
		if g.active[gid] == 1 {
			delete(g.active, gid)
		} else {
			g.active[gid]--
		}
		g.mu.Unlock()
	}
}

func (g *observerCallbackGuard) panicIfActive() {
	gid := currentGoroutineID()
	g.mu.Lock()
	active := g.active[gid] > 0
	g.mu.Unlock()
	if active {
		panic(reentrantObserverPanic)
	}
}

// observerLock is sync.Mutex plus self-reentrancy detection. It exists
// because observerMu (see Directory's field doc) must stay locked for the
// duration of an Observer callback to preserve callback-delivery order —
// but that means a callback that calls back into Register/Remove/RemoveAll/
// SetObserver on the *same* Directory (the same goroutine, trying to lock
// observerMu again) would otherwise block forever: sync.Mutex isn't
// reentrant, and there is no other goroutine that will ever release it.
// That failure mode is a silent hang with no error, no stack, nothing in
// the logs — the worst kind of bug to debug in production. observerLock
// turns it into an immediate, clearly-worded panic instead, by recording
// which goroutine currently holds the lock and checking, before blocking,
// whether the caller already is that goroutine.
//
// This does not affect legitimate cross-goroutine contention: two different
// goroutines racing to both call Register (or Remove/SetObserver) still
// block on each other exactly as sync.Mutex would, since the check only
// fires when the *calling* goroutine's id matches the current holder's.
type observerLock struct {
	mu    sync.Mutex
	owner atomic.Int64 // 0 means unheld; goroutine ids are never 0
}

func (l *observerLock) Lock() {
	if gid := currentGoroutineID(); l.owner.Load() == gid {
		panic(reentrantObserverPanic)
	}
	l.mu.Lock()
	l.owner.Store(currentGoroutineID())
}

func (l *observerLock) Unlock() {
	l.owner.Store(0)
	l.mu.Unlock()
}

// currentGoroutineID parses the calling goroutine's id out of its own stack
// trace header ("goroutine 123 [running]:"). This is the standard technique
// for this in Go, which deliberately does not expose goroutine ids through
// any supported API — the runtime treats them as an implementation detail,
// not an identity applications should build on for anything long-lived.
// Its use here is intentionally narrow: the id is retained only for the
// duration of one Observer callback or observerLock critical section, purely
// to detect immediate same-goroutine reentrancy. It is never treated as a
// long-lived application identity where goroutine id reuse could matter.
func currentGoroutineID() int64 {
	buf := make([]byte, 64)
	buf = buf[:runtime.Stack(buf, false)]
	buf = bytes.TrimPrefix(buf, []byte("goroutine "))
	if idx := bytes.IndexByte(buf, ' '); idx >= 0 {
		buf = buf[:idx]
	}
	id, err := strconv.ParseInt(string(buf), 10, 64)
	if err != nil {
		panic(fmt.Sprintf("dbstore: could not parse goroutine id from stack header: %v", err))
	}
	return id
}
