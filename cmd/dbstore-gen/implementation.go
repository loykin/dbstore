package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// inspectBackend finds production methods declared for receiverName across
// the package. Implementations may be split or renamed; file layout is not a
// correctness requirement after the initial scaffold.
func inspectBackend(dir, receiverName string) (exists bool, methods map[string]struct{}, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, nil, fmt.Errorf("read package directory %s: %w", dir, err)
	}
	methods = make(map[string]struct{})
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		f, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			return false, nil, fmt.Errorf("parse %s: %w", path, parseErr)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 {
				continue
			}
			if receiverTypeName(fn.Recv.List[0].Type) != receiverName {
				continue
			}
			exists = true
			methods[fn.Name.Name] = struct{}{}
		}
	}
	return exists, methods, nil
}

func receiverTypeName(expr ast.Expr) string {
	switch x := expr.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.StarExpr:
		return receiverTypeName(x.X)
	case *ast.IndexExpr:
		return receiverTypeName(x.X)
	case *ast.IndexListExpr:
		return receiverTypeName(x.X)
	default:
		return ""
	}
}

func missingMethodNames(want []methodView, have map[string]struct{}) []string {
	var missing []string
	for _, method := range want {
		if _, ok := have[method.Name]; !ok {
			missing = append(missing, method.Name)
		}
	}
	sort.Strings(missing)
	return missing
}

func missingMethods(want []methodView, have map[string]struct{}) []methodView {
	var missing []methodView
	for _, method := range want {
		if _, ok := have[method.Name]; !ok {
			missing = append(missing, method)
		}
	}
	return missing
}

// renderMissingMethodStubs returns copy-ready methods for an existing backend
// implementation. The generator never edits application-owned backend files,
// but its failure must still tell the user exactly what to add.
func renderMissingMethodStubs(backend backendView, methods []methodView) string {
	var out strings.Builder
	for i, method := range methods {
		if i > 0 {
			out.WriteString("\n\n")
		}
		fmt.Fprintf(&out, "func (%s) %s(ctx context.Context, h %s.Handle", backend.BackendStructName, method.Name, backend.PkgName)
		for _, param := range method.Params {
			paramType := param.Type
			if param.Variadic {
				paramType = "..." + paramType
			}
			fmt.Fprintf(&out, ", %s %s", param.Name, paramType)
		}
		fmt.Fprintf(&out, ") %s {\n\tpanic(\"TODO: implement\")\n}", method.ReturnSig)
	}
	return out.String()
}
