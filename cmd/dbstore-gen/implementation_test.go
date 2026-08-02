package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInspectTemplate_FindsMethodsAcrossRenamedFiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"sqlite_create.go": "package fixture\ntype SqliteUserTemplate struct{}\nfunc (SqliteUserTemplate) Create() {}\n",
		"sqlite_find.go":   "package fixture\nfunc (*SqliteUserTemplate) Find() {}\n",
		"ignored_test.go":  "package fixture\nfunc (SqliteUserTemplate) TestOnly() {}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	exists, methods, err := inspectTemplate(dir, "SqliteUserTemplate")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("template was not found")
	}
	if _, ok := methods["Create"]; !ok {
		t.Fatal("Create method was not found")
	}
	if _, ok := methods["Find"]; !ok {
		t.Fatal("Find method was not found")
	}
	if _, ok := methods["TestOnly"]; ok {
		t.Fatal("test-only method must not satisfy a production template")
	}
}

func TestMissingMethodNames(t *testing.T) {
	want := []methodView{{Name: "Create"}, {Name: "Delete"}, {Name: "Find"}}
	have := map[string]struct{}{"Create": {}, "Find": {}}
	missing := missingMethodNames(want, have)
	if len(missing) != 1 || missing[0] != "Delete" {
		t.Fatalf("missing = %v, want [Delete]", missing)
	}
}
