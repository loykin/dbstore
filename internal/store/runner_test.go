package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

type fakeRunner[T any] struct {
	client T
	runErr error
}

func (f *fakeRunner[T]) Run(ctx context.Context, fn func(context.Context, T) error) error {
	if f.runErr != nil {
		return f.runErr
	}
	return fn(ctx, f.client)
}

func TestCall_Success(t *testing.T) {
	src := &fakeRunner[int]{client: 7}
	got, err := Call(context.Background(), src, func(_ context.Context, c int) (string, error) {
		return fmt.Sprintf("v%d", c), nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "v7" {
		t.Fatalf("got %q, want v7", got)
	}
}

func TestCall_NotFoundReturnsZeroAndError(t *testing.T) {
	src := &fakeRunner[int]{client: 7}
	got, err := Call(context.Background(), src, func(_ context.Context, _ int) (*string, error) {
		return nil, fmt.Errorf("wrapped: %w", ErrNotFound)
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if got != nil {
		t.Fatalf("want returned nil value, got %v", got)
	}
}

func TestCall_OtherErrorPassesThrough(t *testing.T) {
	sentinel := errors.New("boom")
	src := &fakeRunner[int]{client: 7}
	_, err := Call(context.Background(), src, func(_ context.Context, _ int) (int, error) {
		return 0, sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel error, got %v", err)
	}
}

func TestExec_Delegates(t *testing.T) {
	called := false
	src := &fakeRunner[int]{client: 1}
	err := Exec(context.Background(), src, func(_ context.Context, c int) error {
		called = true
		if c != 1 {
			t.Fatalf("got client %d, want 1", c)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("fn was not called")
	}
}

func TestExec_RunnerError(t *testing.T) {
	sentinel := errors.New("run failed")
	src := &fakeRunner[int]{runErr: sentinel}
	err := Exec(context.Background(), src, func(_ context.Context, _ int) error {
		t.Fatal("fn should not be called when Run itself fails")
		return nil
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel error, got %v", err)
	}
}
