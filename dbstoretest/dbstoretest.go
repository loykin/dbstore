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

// Capabilities declares which optional guarantees a fixture's backend
// actually provides, so one compliance suite can assert a guarantee only
// against the fixtures that can honor it instead of a whole suite being
// forced to skip the assertion because one backend can't (or the suite
// silently forking into two different suites — see docs/design-codegen.md's
// "CreateBatch 보장 범위 불일치").
type Capabilities struct {
	// AtomicBatch is true when a multi-record write (e.g. CreateBatch)
	// is all-or-nothing for this fixture. A SQL backend with a real
	// transaction sets this true; a backend with no transaction concept
	// (e.g. REST without a batch endpoint) leaves it false, and the
	// suite skips the atomicity assertion for that fixture instead of
	// either failing it or omitting the assertion for every fixture.
	AtomicBatch bool
}

// Fixture names one backend-specific way to construct a repository
// implementation for RunComplianceSuite.
type Fixture[R any] struct {
	// Name becomes the subtest name (t.Run), e.g. "SQLite" or "REST".
	Name string
	// New constructs a fresh repository instance for one subtest. Called
	// once per t.Run inside the suite, the same way the fixture functions
	// in examples/repo_compliance are.
	New func(t *testing.T) R
	// Caps declares this fixture's optional guarantees. The zero value
	// (every capability false) is correct for a fixture that offers none.
	Caps Capabilities
}

// RunComplianceSuite runs suite once per fixture, each as its own t.Run
// named after the fixture — the loop every runUserRepoComplianceSuite-style
// caller (see examples/repo_compliance) would otherwise write by hand. Each
// fixture's Capabilities is passed through so suite can gate
// capability-dependent assertions (e.g. only run a CreateBatch rollback
// check for fixtures with Caps.AtomicBatch set) without maintaining a
// second, differently-scoped suite function for backends that can't
// support every assertion.
//
//	dbstoretest.RunComplianceSuite(t, []dbstoretest.Fixture[UserRepository]{
//		{Name: "SQLite", New: sqliteFixture, Caps: dbstoretest.Capabilities{AtomicBatch: true}},
//		{Name: "REST", New: restFixture},
//	}, runUserRepoComplianceSuite)
func RunComplianceSuite[R any](t *testing.T, fixtures []Fixture[R], suite func(t *testing.T, newRepo func(t *testing.T) R, caps Capabilities)) {
	t.Helper()
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			suite(t, fixture.New, fixture.Caps)
		})
	}
}
