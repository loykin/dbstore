package store

import (
	"sync"
	"time"
)

type directoryEntry[T any] struct {
	client    T
	throttle  *Throttle
	createdAt time.Time
	wg        sync.WaitGroup // tracks in-flight operation count
}
