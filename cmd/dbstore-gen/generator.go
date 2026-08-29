package main

import (
	"bytes"
	"fmt"
	"go/format"
	"go/token"
	"sort"
	"strings"
	"text/template"
	"unicode"

	"golang.org/x/tools/go/packages"
)

// Backend is one -backend name:importpath flag, resolved to the adapter
// package's actual name (e.g. "sqlxadapter") and confirmed to export the
// Handle and Source types used by generated backend methods and constructors.
type Backend struct {
	Name       string // -backend flag's left side, e.g. "sqlite"
	ImportPath string
	PkgName    string
}

func resolveBackend(name, importPath string) (Backend, error) {
	if !token.IsIdentifier(name) {
		return Backend{}, fmt.Errorf("backend name %q is not a valid Go identifier", name)
	}
	cfg := &packages.Config{Mode: packages.NeedName | packages.NeedTypes}
	pkgs, err := packages.Load(cfg, importPath)
	if err != nil {
		return Backend{}, fmt.Errorf("load backend package %s: %w", importPath, err)
	}
	if len(pkgs) == 0 || pkgs[0].Types == nil {
		return Backend{}, fmt.Errorf("backend package %s not found", importPath)
	}
	pkg := pkgs[0]
	if len(pkg.Errors) > 0 {
		return Backend{}, fmt.Errorf("backend package %s has errors: %v", importPath, pkg.Errors)
	}
	if pkg.Types.Scope().Lookup("Handle") == nil {
		return Backend{}, fmt.Errorf("backend package %s does not export a Handle type — every adapter package used by dbstore-gen must export Handle and Source", importPath)
	}
	if pkg.Types.Scope().Lookup("Source") == nil {
		return Backend{}, fmt.Errorf("backend package %s does not export a Source type — every adapter package used by dbstore-gen must export Handle and Source", importPath)
	}
	return Backend{Name: name, ImportPath: importPath, PkgName: pkg.Name}, nil
}

// baseName strips a trailing "Repository" so generated identifiers read as
// UserRepoBackend/userRepo/NewUserRepo instead of
// UserRepositoryRepoBackend. Interfaces not named *Repository keep their
// full name as the base.
func baseName(ifaceName string) string {
	const suffix = "Repository"
	if strings.HasSuffix(ifaceName, suffix) && len(ifaceName) > len(suffix) {
		return strings.TrimSuffix(ifaceName, suffix)
	}
	return ifaceName
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

type methodView struct {
	Name               string
	Params             []Param
	DomainParams       string
	BackendParams      string
	DomainArgsPrefixed string
	ReturnSig          string
	HasValue           bool
	ValueType          string
}

type backendView struct {
	Name               string
	PkgName            string
	ImportPath         string
	BackendStructName  string
	FactoryName        string
	ConnectFactoryName string
	FixtureVarName     string
}

type importView struct {
	Path  string
	Alias string
}

type genView struct {
	Package       string
	InterfaceName string
	Base          string
	WrapperStruct string
	Constructor   string
	Capabilities  string
	Methods       []methodView
	Backends      []backendView
	Imports       []importView
	DomainImports []importView
}

func buildView(iface *Interface, backends []Backend) genView {
	base := baseName(iface.Name)
	v := genView{
		Package:       iface.Package,
		InterfaceName: iface.Name,
		Base:          base,
		WrapperStruct: lowerFirst(base) + "Repo",
		Constructor:   "New" + base + "Repo",
		Capabilities:  lowerFirst(base) + "RepoCapabilities",
	}
	for _, m := range iface.Methods {
		mv := methodView{
			Name:      m.Name,
			Params:    m.Params,
			HasValue:  m.HasValue,
			ValueType: m.ValueType,
		}
		var domainParams, backendParams, argsPrefixed strings.Builder
		domainParams.WriteString("ctx context.Context")
		backendParams.WriteString("ctx context.Context, h A")
		for _, p := range m.Params {
			paramType := p.Type
			arg := p.Name
			if p.Variadic {
				paramType = "..." + paramType
				arg += "..."
			}
			domainParams.WriteString(", " + p.Name + " " + paramType)
			backendParams.WriteString(", " + p.Name + " " + paramType)
			argsPrefixed.WriteString(", " + arg)
		}
		mv.DomainParams = domainParams.String()
		mv.BackendParams = backendParams.String()
		mv.DomainArgsPrefixed = argsPrefixed.String()
		if m.HasValue {
			mv.ReturnSig = "(" + m.ValueType + ", error)"
		} else {
			mv.ReturnSig = "error"
		}
		v.Methods = append(v.Methods, mv)
	}
	imports := map[string]string{
		"github.com/loykin/dbstore": "dbstore",
	}
	usedAliases := map[string]string{
		"context": "context",
		"dbstore": "github.com/loykin/dbstore",
	}
	for path, alias := range iface.Imports {
		imports[path] = alias
		usedAliases[alias] = path
	}

	seenBackendNames := make(map[string]struct{}, len(backends))
	for _, b := range backends {
		if _, exists := seenBackendNames[b.Name]; exists {
			continue
		}
		seenBackendNames[b.Name] = struct{}{}
		alias, ok := imports[b.ImportPath]
		if !ok {
			alias = uniqueImportAlias(b.PkgName, b.ImportPath, usedAliases)
			imports[b.ImportPath] = alias
		}
		v.Backends = append(v.Backends, backendView{
			Name:               b.Name,
			PkgName:            alias,
			ImportPath:         b.ImportPath,
			BackendStructName:  upperFirst(b.Name) + base + "Backend",
			FactoryName:        "New" + upperFirst(b.Name) + iface.Name,
			ConnectFactoryName: "Connect" + upperFirst(b.Name) + iface.Name,
			FixtureVarName:     lowerFirst(upperFirst(b.Name) + base + "Fixture"),
		})
	}

	paths := make([]string, 0, len(imports))
	for path := range imports {
		if path == "context" || path == "github.com/loykin/dbstore" {
			continue
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		v.Imports = append(v.Imports, importView{Path: path, Alias: imports[path]})
		if _, ok := iface.Imports[path]; ok {
			v.DomainImports = append(v.DomainImports, importView{Path: path, Alias: imports[path]})
		}
	}
	return v
}

func renderGenFile(v genView) ([]byte, error) {
	return render(genFileTemplate, v)
}

func renderComplianceSuiteSkeleton(v genView) ([]byte, error) {
	return render(complianceSuiteSkeletonTemplate, v)
}

func renderFixtureStub(v genView, b backendView) ([]byte, error) {
	type stubView struct {
		genView
		Backend backendView
	}
	return render(fixtureStubTemplate, stubView{genView: v, Backend: b})
}

func renderComplianceRegistry(v genView) ([]byte, error) {
	return render(complianceRegistryTemplate, v)
}

func renderBackendStub(v genView, b backendView) ([]byte, error) {
	type stubView struct {
		genView
		Backend backendView
	}
	return render(backendStubTemplate, stubView{genView: v, Backend: b})
}

func render(tmplText string, data any) ([]byte, error) {
	tmpl, err := template.New("gen").Parse(tmplText)
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("render template: %w", err)
	}
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("gofmt generated output: %w\n---\n%s", err, buf.String())
	}
	return formatted, nil
}
