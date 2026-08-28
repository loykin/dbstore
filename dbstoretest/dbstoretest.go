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
// implementation for RunComplianceSuite. C is an application-owned
// capability type describing optional guarantees of that repository
// contract; use struct{} when the contract has no optional guarantees.
type Fixture[R, C any] struct {
	// Name becomes the subtest name (t.Run), e.g. "SQLite" or "REST".
	Name string
	// New constructs a fresh repository instance for one subtest. Called
	// once per t.Run inside the suite, the same way the fixture functions
	// in examples/repo_compliance are.
	New func(t *testing.T) R
	// Caps declares this fixture's optional guarantees. Its meaning belongs
	// to the application repository contract, not to dbstoretest.
	Caps C
}

// RunComplianceSuite runs suite once per fixture, each as its own t.Run
// named after the fixture — the loop every runUserRepoComplianceSuite-style
// caller (see examples/repo_compliance) would otherwise write by hand. Each
// fixture's application-owned capabilities value is passed through so the
// suite can gate capability-dependent assertions (e.g. only run a
// CreateBatch rollback check for fixtures with Caps.AtomicBatch set) without
// maintaining a second, differently-scoped suite function for backends that
// can't support every assertion.
//
//	dbstoretest.RunComplianceSuite(t, []dbstoretest.Fixture[UserRepository, userRepoCapabilities]{
//		{Name: "SQLite", New: sqliteFixture, Caps: userRepoCapabilities{AtomicBatch: true}},
//		{Name: "REST", New: restFixture},
//	}, runUserRepoComplianceSuite)
func RunComplianceSuite[R, C any](t *testing.T, fixtures []Fixture[R, C], suite func(t *testing.T, newRepo func(t *testing.T) R, caps C)) {
	t.Helper()
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			suite(t, fixture.New, fixture.Caps)
		})
	}
}
