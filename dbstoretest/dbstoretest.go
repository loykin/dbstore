// Package dbstoretest gives the "one repository contract, one compliance
// suite, multiple implementations" pattern (see the root README's "Why")
// a piece of actual API surface, instead of leaving it as a convention
// every application re-derives on its own.
//
// It does not — and cannot — know what your repository interface looks
// like or what its correct behavior is; you still write the suite. What it
// removes is the boilerplate loop of running that suite once per backend
// fixture.
package dbstoretest

import "testing"

// Fixture names one backend-specific way to construct a repository
// implementation for RunComplianceSuite.
type Fixture[R any] struct {
	// Name becomes the subtest name (t.Run), e.g. "SQLite" or "REST".
	Name string
	// New constructs a fresh repository instance for one subtest. Called
	// once per t.Run inside the suite, the same way the fixture functions
	// in examples/repo_compliance are.
	New func(t *testing.T) R
}

// RunComplianceSuite runs suite once per fixture, each as its own t.Run
// named after the fixture — the loop every runUserRepoComplianceSuite-style
// caller (see examples/repo_compliance) would otherwise write by hand.
//
//	dbstoretest.RunComplianceSuite(t, []dbstoretest.Fixture[UserRepository]{
//		{Name: "SQLite", New: sqliteFixture},
//		{Name: "REST", New: restFixture},
//	}, runUserRepoComplianceSuite)
func RunComplianceSuite[R any](t *testing.T, fixtures []Fixture[R], suite func(t *testing.T, newRepo func(t *testing.T) R)) {
	t.Helper()
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			suite(t, fixture.New)
		})
	}
}
