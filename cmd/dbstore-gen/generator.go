package main

import (
	"bytes"
	"fmt"
	"go/format"
	"sort"
	"strings"
	"text/template"
	"unicode"

	"golang.org/x/tools/go/packages"
)

// Backend is one -backend name:importpath flag, resolved to the adapter
// package's actual name (e.g. "sqlxadapter") and confirmed to export an
// Adaptor type — the type every adapter package exposes as its
// Runner-satisfying handle (see docs/design-codegen.md).
type Backend struct {
	Name       string // -backend flag's left side, e.g. "sqlite"
	ImportPath string
	PkgName    string
}

func resolveBackend(name, importPath string) (Backend, error) {
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
	if pkg.Types.Scope().Lookup("Adaptor") == nil {
		return Backend{}, fmt.Errorf("backend package %s does not export an Adaptor type — every adapter package must (see adapters/sqlx/adaptor.go)", importPath)
	}
	return Backend{Name: name, ImportPath: importPath, PkgName: pkg.Name}, nil
}

// baseName strips a trailing "Repository" so generated identifiers read as
// UserRepoTemplate/userRepo/NewUserRepo instead of
// UserRepositoryRepoTemplate. Interfaces not named *Repository keep their
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
	TemplateParams     string
	DomainArgsPrefixed string
	ReturnSig          string
	HasValue           bool
	ValueType          string
}

type backendView struct {
	Name               string
	PkgName            string
	ImportPath         string
	TemplateStructName string
}

type genView struct {
	Package       string
	InterfaceName string
	Base          string
	WrapperStruct string
	Constructor   string
	Methods       []methodView
	Backends      []backendView
	ExtraImports  []struct{ Path, Alias string }
}

func buildView(iface *Interface, backends []Backend) genView {
	base := baseName(iface.Name)
	v := genView{
		Package:       iface.Package,
		InterfaceName: iface.Name,
		Base:          base,
		WrapperStruct: lowerFirst(base) + "Repo",
		Constructor:   "New" + base + "Repo",
	}
	for _, m := range iface.Methods {
		mv := methodView{
			Name:      m.Name,
			Params:    m.Params,
			HasValue:  m.HasValue,
			ValueType: m.ValueType,
		}
		var domainParams, templateParams, argsPrefixed strings.Builder
		domainParams.WriteString("ctx context.Context")
		templateParams.WriteString("ctx context.Context, a A")
		for _, p := range m.Params {
			domainParams.WriteString(", " + p.Name + " " + p.Type)
			templateParams.WriteString(", " + p.Name + " " + p.Type)
			argsPrefixed.WriteString(", " + p.Name)
		}
		mv.DomainParams = domainParams.String()
		mv.TemplateParams = templateParams.String()
		mv.DomainArgsPrefixed = argsPrefixed.String()
		if m.HasValue {
			mv.ReturnSig = "(" + m.ValueType + ", error)"
		} else {
			mv.ReturnSig = "error"
		}
		v.Methods = append(v.Methods, mv)
	}
	for _, b := range backends {
		v.Backends = append(v.Backends, backendView{
			Name:               b.Name,
			PkgName:            b.PkgName,
			ImportPath:         b.ImportPath,
			TemplateStructName: upperFirst(b.Name) + base + "Template",
		})
	}

	paths := make([]string, 0, len(iface.Imports))
	for path := range iface.Imports {
		if path == "context" {
			continue
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		v.ExtraImports = append(v.ExtraImports, struct{ Path, Alias string }{Path: path, Alias: iface.Imports[path]})
	}
	return v
}

func renderGenFile(v genView) ([]byte, error) {
	return render(genFileTemplate, v)
}

func renderTestSkeleton(v genView) ([]byte, error) {
	return render(testSkeletonTemplate, v)
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
