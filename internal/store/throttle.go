package store

import "context"

type Throttle struct {
	sem chan struct{}
}

func newThrottle(maxConcurrency int) *Throttle {
	if maxConcurrency <= 0 {
		return &Throttle{}
	}
	sem := make(chan struct{}, maxConcurrency)
	for i := 0; i < maxConcurrency; i++ {
		sem <- struct{}{}
	}
	return &Throttle{sem: sem}
}

func (t *Throttle) Acquire(ctx context.Context) error {
	// check before the nil-sem fast-path and the select race
	if err := ctx.Err(); err != nil {
		return err
	}
	if t.sem == nil {
		return nil
	}
	select {
	case <-t.sem:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *Throttle) Release() {
	if t.sem != nil {
		t.sem <- struct{}{}
	}
}
