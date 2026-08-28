package main

import (
	"strings"
	"testing"
)

const fixtureFile = "testdata/fixture/user_repo.go"

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

func TestParseInterface_SupportsVariadic(t *testing.T) {
	iface, err := parseInterface(fixtureFile, "VariadicRepository")
	if err != nil {
		t.Fatal(err)
	}
	param := iface.Methods[0].Params[0]
	if !param.Variadic || param.Type != "string" {
		t.Fatalf("param = %+v, want variadic string", param)
	}
}

func TestParseInterface_RejectsMoreThanTwoReturns(t *testing.T) {
	_, err := parseInterface(fixtureFile, "MultiReturnRepository")
	if err == nil || !strings.Contains(err.Error(), "return values") {
		t.Fatalf("err = %v, want return-count rejection", err)
	}
}

func TestParseInterface_SupportsNamedReturns(t *testing.T) {
	iface, err := parseInterface(fixtureFile, "NamedReturnRepository")
	if err != nil {
		t.Fatal(err)
	}
	if !iface.Methods[0].HasValue || iface.Methods[0].ValueType != "*User" {
		t.Fatalf("method = %+v, want (*User, error)", iface.Methods[0])
	}
}

func TestParseInterface_RejectsMissingContext(t *testing.T) {
	_, err := parseInterface(fixtureFile, "NoContextRepository")
	if err == nil || !strings.Contains(err.Error(), "context.Context") {
		t.Fatalf("err = %v, want context.Context rejection", err)
	}
}

func TestParseInterface_SupportsEmbeddedInterface(t *testing.T) {
	iface, err := parseInterface(fixtureFile, "EmbeddingRepository")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Create", "CreateBatch", "Extra", "FindByID"}
	if len(iface.Methods) != len(want) {
		t.Fatalf("methods = %+v, want %v", iface.Methods, want)
	}
	for i, method := range iface.Methods {
		if method.Name != want[i] {
			t.Fatalf("method[%d] = %q, want %q", i, method.Name, want[i])
		}
	}
}

func TestParseInterface_UnknownInterfaceErrors(t *testing.T) {
	_, err := parseInterface(fixtureFile, "DoesNotExist")
	if err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("err = %v, want not-declared error", err)
	}
	// Error should list what actually is declared — the only thing that
	// used to be "found by guessing" now has to be findable by reading.
	if err != nil && !strings.Contains(err.Error(), "UserRepository") {
		t.Fatalf("err = %v, want it to list declared interfaces", err)
	}
}
