package store

import (
	"sync"
	"time"
)

type directoryEntry[T any] struct {
	client         T
	driver         string
	throttle       *Throttle
	maxConcurrency int
	createdAt      time.Time
	wg             sync.WaitGroup // tracks in-flight operation count
}
