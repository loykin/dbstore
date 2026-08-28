package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseBackendSpec_Builtins(t *testing.T) {
	cases := map[string]struct {
		name string
		path string
	}{
		"sqlite":       {name: "sqlite", path: "github.com/loykin/dbstore/adapters/sqlx"},
		"postgres":     {name: "postgres", path: "github.com/loykin/dbstore/adapters/sqlx"},
		"rest":         {name: "rest", path: "github.com/loykin/dbstore/adapters/rest"},
		"primary:sqlx": {name: "primary", path: "github.com/loykin/dbstore/adapters/sqlx"},
	}
	for spec, want := range cases {
		name, path, err := parseBackendSpec(spec)
		if err != nil {
			t.Fatalf("parseBackendSpec(%q): %v", spec, err)
		}
		if name != want.name || path != want.path {
			t.Fatalf("parseBackendSpec(%q) = (%q, %q), want (%q, %q)", spec, name, path, want.name, want.path)
		}
	}
}

func TestParseBackendSpec_CustomImport(t *testing.T) {
	name, path, err := parseBackendSpec("legacy:example.com/acme/adapter")
	if err != nil {
		t.Fatal(err)
	}
	if name != "legacy" || path != "example.com/acme/adapter" {
		t.Fatalf("got (%q, %q)", name, path)
	}
}

func TestRun_ConfigDrivesInterfaceSourceAndBackends(t *testing.T) {
	dir := t.TempDir()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	goMod := "module example.com/fixture\n\ngo 1.26.4\n\nrequire github.com/loykin/dbstore v0.0.0\n\nreplace github.com/loykin/dbstore => " + root + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "user_repo.go")
	content := "package fixture\n\nimport \"context\"\n\ntype UserRepository interface {\n\tCreate(ctx context.Context, name string) error\n}\n"
	if err := os.WriteFile(source, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	configPath := filepath.Join(dir, "user_repo.gen.yaml")
	configContent := "interface: UserRepository\nsource: user_repo.go\ntest: true\nbackends:\n  - name: sqlite\n    adapter: sqlite\n"
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := run([]string{"-config", configPath}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "user_repo_gen.go")); err != nil {
		t.Fatalf("gen file not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "user_repo_sqlite.go")); err != nil {
		t.Fatalf("backend stub not created: %v", err)
	}
	for _, name := range []string{
		"user_repo_compliance_test.go",
		"user_repo_sqlite_test.go",
		"user_repo_compliance_gen_test.go",
	} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s not created even though config set test: true: %v", name, err)
		}
	}

	// Application-owned suite and fixture files survive backend growth, while
	// the generated registry expands and a new backend fixture is scaffolded.
	suitePath := filepath.Join(dir, "user_repo_compliance_test.go")
	sqliteFixturePath := filepath.Join(dir, "user_repo_sqlite_test.go")
	for _, path := range []string{suitePath, sqliteFixturePath} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		content = append(content, []byte("\n// application-owned marker\n")...)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	configContent = "interface: UserRepository\nsource: user_repo.go\ntest: true\nbackends:\n  - name: sqlite\n    adapter: sqlite\n  - name: rest\n    adapter: rest\n"
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-config", configPath}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{suitePath, sqliteFixturePath} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), "application-owned marker") {
			t.Fatalf("application-owned file %s was overwritten", path)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "user_repo_rest_test.go")); err != nil {
		t.Fatalf("new backend fixture was not scaffolded: %v", err)
	}
	registry, err := os.ReadFile(filepath.Join(dir, "user_repo_compliance_gen_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []string{"sqliteUserFixture", "restUserFixture"} {
		if !strings.Contains(string(registry), fixture) {
			t.Fatalf("generated registry missing %s\n---\n%s", fixture, registry)
		}
	}
}

func TestRun_ConfigCannotBeCombinedWithInterfaceOrBackend(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "user_repo.gen.yaml")
	if err := os.WriteFile(configPath, []byte("interface: UserRepository\nsource: user_repo.go\nbackends:\n  - name: sqlite\n    adapter: sqlite\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := run([]string{"-config", configPath, "-interface", "UserRepository"})
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("err = %v, want cannot-combine error for -interface", err)
	}

	err = run([]string{"-config", configPath, "-backend", "sqlite"})
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("err = %v, want cannot-combine error for -backend", err)
	}
}

func TestRun_InterfaceAndBackendGrowthPreserveExistingImplementation(t *testing.T) {
	dir := t.TempDir()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	goMod := "module example.com/fixture\n\ngo 1.26.4\n\nrequire github.com/loykin/dbstore v0.0.0\n\nreplace github.com/loykin/dbstore => " + root + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "user_repo.go")
	writeSource := func(methods string) {
		t.Helper()
		content := "package fixture\n\nimport \"context\"\n\ntype UserRepository interface {\n" + methods + "}\n"
		if err := os.WriteFile(source, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeSource("Create(ctx context.Context, name string) error\n")

	baseArgs := []string{"-source", source, "-interface", "UserRepository", "-backend", "sqlite"}
	if err := run(baseArgs); err != nil {
		t.Fatal(err)
	}
	stubPath := filepath.Join(dir, "user_repo_sqlite.go")
	stub, err := os.ReadFile(stubPath)
	if err != nil {
		t.Fatal(err)
	}
	stub = append(stub, []byte("\n// preserved implementation marker\n")...)
	if err := os.WriteFile(stubPath, stub, 0o600); err != nil {
		t.Fatal(err)
	}
	genPath := filepath.Join(dir, "user_repo_gen.go")
	genBefore, err := os.ReadFile(genPath)
	if err != nil {
		t.Fatal(err)
	}

	writeSource("Create(ctx context.Context, name string) error\nDelete(ctx context.Context, id int) error\n")
	err = run(baseArgs)
	if err == nil || !strings.Contains(err.Error(), "missing methods: Delete") {
		t.Fatalf("err = %v, want missing Delete diagnostic", err)
	}
	if !strings.Contains(err.Error(), "func (SqliteUserBackend) Delete(ctx context.Context, h sqlxadapter.Handle, id int) error") {
		t.Fatalf("err = %v, want copy-ready Delete stub", err)
	}
	genAfterFailure, err := os.ReadFile(genPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(genAfterFailure) != string(genBefore) {
		t.Fatal("generated glue changed even though an existing backend was incomplete")
	}
	preserved, err := os.ReadFile(stubPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(preserved), "preserved implementation marker") {
		t.Fatal("existing backend implementation was overwritten")
	}

	stubWithDelete := append(preserved, []byte("\nfunc (SqliteUserBackend) Delete(ctx context.Context, a sqlxadapter.Handle, id int) error { return nil }\n")...)
	if err := os.WriteFile(stubPath, stubWithDelete, 0o600); err != nil {
		t.Fatal(err)
	}
	grownArgs := append(append([]string{}, baseArgs...), "-backend", "rest")
	if err := run(grownArgs); err != nil {
		t.Fatal(err)
	}
	gen, err := os.ReadFile(genPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gen), "func (r *userRepo[A]) Delete(") {
		t.Fatal("generated glue does not contain the new repository method")
	}
	if _, err := os.Stat(filepath.Join(dir, "user_repo_rest.go")); err != nil {
		t.Fatalf("new backend stub was not created: %v", err)
	}
	finalSQLite, err := os.ReadFile(stubPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(finalSQLite) != string(stubWithDelete) {
		t.Fatal("adding a backend modified the existing backend implementation")
	}
}
