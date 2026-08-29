package store

import (
	"context"
	"testing"
)

func TestSource_DoesNotRebindAfterSameNameReplacement(t *testing.T) {
	registry := NewDriverRegistry[int]()
	registry.Register("first", intDriver(1))
	registry.Register("replacement", intDriver(2))
	directory := NewDirectory(registry, nil)
	defer directory.RemoveAll()

	if err := directory.Register("primary", SourceConfig{Driver: "first"}); err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(directory)
	oldSource := NewSource("primary", exec)

	if err := directory.Remove("primary"); err != nil {
		t.Fatal(err)
	}
	if err := directory.Register("primary", SourceConfig{Driver: "replacement"}); err != nil {
		t.Fatal(err)
	}

	called := false
	err := oldSource.Run(context.Background(), func(context.Context, int) error {
		called = true
		return nil
	})
	if err == nil {
		t.Fatal("old Source unexpectedly rebound to the replacement")
	}
	if called {
		t.Fatal("old Source callback ran against the replacement")
	}

	freshSource := NewSource("primary", exec)
	if err := freshSource.Run(context.Background(), func(_ context.Context, client int) error {
		if client != 2 {
			t.Fatalf("fresh Source client = %d, want replacement 2", client)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Executor.Run remains the explicit low-level live-name operation.
	if err := exec.Run(context.Background(), "primary", func(_ context.Context, client int) error {
		if client != 2 {
			t.Fatalf("Executor client = %d, want replacement 2", client)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSource_ConstructBeforeRegisterDoesNotBindLater(t *testing.T) {
	registry := NewDriverRegistry[int]()
	registry.Register("driver", intDriver(1))
	directory := NewDirectory(registry, nil)
	defer directory.RemoveAll()

	exec := NewExecutor(directory)
	source := NewSource("primary", exec)
	if err := directory.Register("primary", SourceConfig{Driver: "driver"}); err != nil {
		t.Fatal(err)
	}

	called := false
	err := source.Run(context.Background(), func(context.Context, int) error {
		called = true
		return nil
	})
	if err == nil {
		t.Fatal("Source constructed before registration unexpectedly bound later")
	}
	if called {
		t.Fatal("unbound Source callback ran")
	}
}
