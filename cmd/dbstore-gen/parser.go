package main

import (
	"fmt"
	"go/types"
	"path/filepath"
	"sort"

	"golang.org/x/tools/go/packages"
)

// Param is one non-context parameter of a domain interface method.
type Param struct {
	Name string
	Type string
}

// Method is one domain interface method, already validated against the v1
// signature scope: M(ctx, args...) error or M(ctx, args...) (V, error).
type Method struct {
	Name      string
	Params    []Param
	HasValue  bool   // true for the (V, error) shape
	ValueType string // set when HasValue
}

// Interface is the parsed, validated shape of the -interface domain
// contract dbstore-gen mirrors into a Template interface and generic
// wrapper.
type Interface struct {
	Name    string
	Package string // package name the source file declares
	Methods []Method
	Imports map[string]string // import path -> package name, referenced by Param/ValueType strings
}

// parseInterface loads the package containing sourceFile via go/packages
// and extracts ifaceName's method set with full type information, so
// parameter and result types render exactly as declared (including types
// from other packages) rather than as raw source text.
func parseInterface(sourceFile, ifaceName string) (*Interface, error) {
	absDir, err := filepath.Abs(filepath.Dir(sourceFile))
	if err != nil {
		return nil, fmt.Errorf("resolve source dir: %w", err)
	}

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedTypes | packages.NeedTypesInfo |
			packages.NeedSyntax | packages.NeedImports | packages.NeedDeps,
		Dir: absDir,
	}
	pkgs, err := packages.Load(cfg, ".")
	if err != nil {
		return nil, fmt.Errorf("load package: %w", err)
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("no package found for %s", sourceFile)
	}
	pkg := pkgs[0]
	// pkg.Errors is deliberately not treated as fatal here: the most common
	// reason this package fails to type-check is a stale _gen.go from
	// before -interface's last edit (e.g. a Template no longer implements
	// it) — exactly the case this tool exists to fix by regenerating that
	// file. Go's type checker still resolves an unrelated, self-contained
	// interface declaration like ifaceName even when some other file in
	// the package has errors, so only bail out below if that specific
	// lookup actually fails.
	obj := pkg.Types.Scope().Lookup(ifaceName)
	if obj == nil {
		if len(pkg.Errors) > 0 {
			return nil, fmt.Errorf("interface %s not found in package %s, which also has errors: %v", ifaceName, pkg.PkgPath, pkg.Errors)
		}
		return nil, fmt.Errorf("interface %s not found in package %s", ifaceName, pkg.PkgPath)
	}
	named, ok := obj.Type().(*types.Named)
	if !ok {
		return nil, fmt.Errorf("%s is not a named type", ifaceName)
	}
	iface, ok := named.Underlying().(*types.Interface)
	if !ok {
		return nil, fmt.Errorf("%s is not an interface", ifaceName)
	}

	if iface.NumExplicitMethods() != iface.NumMethods() {
		return nil, fmt.Errorf("%s embeds another interface — unsupported in v1, write the Template by hand for this interface", ifaceName)
	}

	imports := map[string]string{}
	qualifier := func(p *types.Package) string {
		if p == nil || p == pkg.Types {
			// nil: builtin. pkg.Types: the interface's own package — types
			// declared there (e.g. *User) need no qualifier and must not
			// be added to imports, or the generated file would import
			// itself.
			return ""
		}
		imports[p.Path()] = p.Name()
		return p.Name()
	}

	methods := make([]Method, 0, iface.NumMethods())
	// Sort by name for deterministic output — *types.Interface does not
	// guarantee declaration order.
	names := make([]string, iface.NumMethods())
	byName := map[string]*types.Func{}
	for i := 0; i < iface.NumMethods(); i++ {
		f := iface.Method(i)
		names[i] = f.Name()
		byName[f.Name()] = f
	}
	sort.Strings(names)

	for _, name := range names {
		f := byName[name]
		sig := f.Type().(*types.Signature)
		m, err := parseMethod(name, sig, qualifier)
		if err != nil {
			return nil, fmt.Errorf("method %s.%s: %w (write this Template method by hand instead)", ifaceName, name, err)
		}
		methods = append(methods, m)
	}

	return &Interface{
		Name:    ifaceName,
		Package: pkg.Name,
		Methods: methods,
		Imports: imports,
	}, nil
}

func parseMethod(name string, sig *types.Signature, qualifier types.Qualifier) (Method, error) {
	if sig.Variadic() {
		return Method{}, fmt.Errorf("variadic parameters are unsupported in v1")
	}

	params := sig.Params()
	if params.Len() == 0 {
		return Method{}, fmt.Errorf("must take context.Context as its first parameter")
	}
	if types.TypeString(params.At(0).Type(), qualifier) != "context.Context" {
		return Method{}, fmt.Errorf("first parameter must be context.Context, got %s", types.TypeString(params.At(0).Type(), qualifier))
	}

	m := Method{Name: name}
	for i := 1; i < params.Len(); i++ {
		p := params.At(i)
		pname := p.Name()
		if pname == "" {
			pname = fmt.Sprintf("arg%d", i)
		}
		m.Params = append(m.Params, Param{Name: pname, Type: types.TypeString(p.Type(), qualifier)})
	}

	results := sig.Results()
	switch results.Len() {
	case 1:
		if types.TypeString(results.At(0).Type(), qualifier) != "error" {
			return Method{}, fmt.Errorf("single return value must be error, got %s", types.TypeString(results.At(0).Type(), qualifier))
		}
	case 2:
		if results.At(0).Name() != "" || results.At(1).Name() != "" {
			return Method{}, fmt.Errorf("named return values are unsupported in v1")
		}
		if types.TypeString(results.At(1).Type(), qualifier) != "error" {
			return Method{}, fmt.Errorf("second return value must be error, got %s", types.TypeString(results.At(1).Type(), qualifier))
		}
		m.HasValue = true
		m.ValueType = types.TypeString(results.At(0).Type(), qualifier)
	default:
		return Method{}, fmt.Errorf("%d return values is unsupported in v1 (supported: error, or (V, error))", results.Len())
	}

	return m, nil
}
