package dbstoretest

import (
	"testing"
)

type stubRepo struct{ label string }
type testCapabilities struct{ Atomic bool }

func TestRunComplianceSuite_RunsOncePerFixtureAsNamedSubtests(t *testing.T) {
	var seen []string

	fixtures := []Fixture[*stubRepo, struct{}]{
		{Name: "First", New: func(t *testing.T) *stubRepo { return &stubRepo{label: "first"} }},
		{Name: "Second", New: func(t *testing.T) *stubRepo { return &stubRepo{label: "second"} }},
	}

	RunComplianceSuite(t, fixtures, func(t *testing.T, newRepo func(t *testing.T) *stubRepo, _ struct{}) {
		repo := newRepo(t)
		seen = append(seen, t.Name(), repo.label)
	})

	want := []string{
		"TestRunComplianceSuite_RunsOncePerFixtureAsNamedSubtests/First", "first",
		"TestRunComplianceSuite_RunsOncePerFixtureAsNamedSubtests/Second", "second",
	}
	if len(seen) != len(want) {
		t.Fatalf("seen = %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("seen[%d] = %q, want %q", i, seen[i], want[i])
		}
	}
}

func TestRunComplianceSuite_NewCalledFreshPerSubtestInvocation(t *testing.T) {
	var calls int
	fixtures := []Fixture[*stubRepo, struct{}]{
		{Name: "Only", New: func(t *testing.T) *stubRepo {
			calls++
			return &stubRepo{}
		}},
	}

	RunComplianceSuite(t, fixtures, func(t *testing.T, newRepo func(t *testing.T) *stubRepo, _ struct{}) {
		t.Run("A", func(t *testing.T) { newRepo(t) })
		t.Run("B", func(t *testing.T) { newRepo(t) })
	})

	if calls != 2 {
		t.Fatalf("New called %d times, want 2 (once per t.Run inside suite)", calls)
	}
}

func TestRunComplianceSuite_PassesFixtureCapabilitiesThrough(t *testing.T) {
	var got []testCapabilities
	fixtures := []Fixture[*stubRepo, testCapabilities]{
		{Name: "Atomic", New: func(t *testing.T) *stubRepo { return &stubRepo{} }, Caps: testCapabilities{Atomic: true}},
		{Name: "BestEffort", New: func(t *testing.T) *stubRepo { return &stubRepo{} }},
	}

	RunComplianceSuite(t, fixtures, func(t *testing.T, newRepo func(t *testing.T) *stubRepo, caps testCapabilities) {
		got = append(got, caps)
	})

	if len(got) != 2 || !got[0].Atomic || got[1].Atomic {
		t.Fatalf("got %+v, want [{Atomic:true} {Atomic:false}]", got)
	}
}
