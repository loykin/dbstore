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

// inspectTemplate finds production methods declared for receiverName across
// the package. Implementations may be split or renamed; file layout is not a
// correctness requirement after the initial scaffold.
func inspectTemplate(dir, receiverName string) (exists bool, methods map[string]struct{}, err error) {
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
