package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixtureFile = "testdata/fixture/user_repo.go"

func TestInferInterfaceName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.go")
	content := []byte("package fixture\n\ntype helper interface{ Help() }\ntype UserRepository interface{ Find() error }\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := inferInterfaceName(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != "UserRepository" {
		t.Fatalf("got %q, want UserRepository", got)
	}
}

func TestInferInterfaceName_Ambiguous(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.go")
	content := []byte("package fixture\n\ntype UserRepository interface{}\ntype AuditRepository interface{}\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := inferInterfaceName(path)
	if err == nil || !strings.Contains(err.Error(), "pass -interface explicitly") {
		t.Fatalf("err = %v, want explicit-interface guidance", err)
	}
}

func TestParseInterface_ExtractsMethodsSortedByName(t *testing.T) {
	iface, err := parseInterface(fixtureFile, "UserRepository")
	if err != nil {
		t.Fatal(err)
	}
	if iface.Name != "UserRepository" {
		t.Fatalf("Name = %q", iface.Name)
	}
	if iface.Package != "fixture" {
		t.Fatalf("Package = %q, want fixture", iface.Package)
	}
	if len(iface.Methods) != 3 {
		t.Fatalf("got %d methods, want 3: %+v", len(iface.Methods), iface.Methods)
	}
	// Create, CreateBatch, FindByID — alphabetical, deterministic output.
	wantNames := []string{"Create", "CreateBatch", "FindByID"}
	for i, m := range iface.Methods {
		if m.Name != wantNames[i] {
			t.Fatalf("Methods[%d].Name = %q, want %q", i, m.Name, wantNames[i])
		}
	}

	create := iface.Methods[0]
	if create.HasValue {
		t.Fatal("Create should be error-only")
	}
	if len(create.Params) != 1 || create.Params[0].Name != "name" || create.Params[0].Type != "string" {
		t.Fatalf("Create.Params = %+v", create.Params)
	}

	findByID := iface.Methods[2]
	if !findByID.HasValue || findByID.ValueType != "*User" {
		t.Fatalf("FindByID: HasValue=%v ValueType=%q, want true, *User (unqualified — User is in the interface's own package)", findByID.HasValue, findByID.ValueType)
	}
}

func TestParseInterface_RejectsVariadic(t *testing.T) {
	_, err := parseInterface(fixtureFile, "VariadicRepository")
	if err == nil || !strings.Contains(err.Error(), "variadic") {
		t.Fatalf("err = %v, want variadic rejection", err)
	}
}

func TestParseInterface_RejectsMoreThanTwoReturns(t *testing.T) {
	_, err := parseInterface(fixtureFile, "MultiReturnRepository")
	if err == nil || !strings.Contains(err.Error(), "return values") {
		t.Fatalf("err = %v, want return-count rejection", err)
	}
}

func TestParseInterface_RejectsNamedReturns(t *testing.T) {
	_, err := parseInterface(fixtureFile, "NamedReturnRepository")
	if err == nil || !strings.Contains(err.Error(), "named return") {
		t.Fatalf("err = %v, want named-return rejection", err)
	}
}

func TestParseInterface_RejectsMissingContext(t *testing.T) {
	_, err := parseInterface(fixtureFile, "NoContextRepository")
	if err == nil || !strings.Contains(err.Error(), "context.Context") {
		t.Fatalf("err = %v, want context.Context rejection", err)
	}
}

func TestParseInterface_RejectsEmbeddedInterface(t *testing.T) {
	_, err := parseInterface(fixtureFile, "EmbeddingRepository")
	if err == nil || !strings.Contains(err.Error(), "embeds another interface") {
		t.Fatalf("err = %v, want embedding rejection", err)
	}
}

func TestParseInterface_UnknownInterfaceErrors(t *testing.T) {
	_, err := parseInterface(fixtureFile, "DoesNotExist")
	if err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("err = %v, want not-declared error", err)
	}
}
