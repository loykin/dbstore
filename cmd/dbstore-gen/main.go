// Command dbstore-gen mirrors a hand-written domain interface into the
// generic wiring a multi-backend repository needs (Template[A] interface +
// generic wrapper), and scaffolds a compliance-test skeleton and one
// Template stub per backend the first time they're needed. It deliberately
// leaves structural safety to the Go type system and behavioral portability
// to dbstoretest instead of duplicating either in the generator.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// backendFlags collects repeated -backend name[:adapter] flags.
type backendFlags []string

func (b *backendFlags) String() string { return strings.Join(*b, ",") }
func (b *backendFlags) Set(v string) error {
	*b = append(*b, v)
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "dbstore-gen:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("dbstore-gen", flag.ContinueOnError)
	ifaceName := fs.String("interface", "", "domain interface name to mirror (inferred when the source has one repository interface)")
	source := fs.String("source", os.Getenv("GOFILE"), "Go file declaring -interface (defaults to GOFILE under go generate)")
	out := fs.String("out", "", "output file for the generated glue (default: <source base>_gen.go)")
	generateTest := fs.Bool("test", false, "create a compliance-test skeleton if it does not exist")
	var backends backendFlags
	fs.Var(&backends, "backend", "name[:adapter], repeatable; built-ins: sqlite, mysql, postgres, sqlx, rest, opensearch, elasticsearch")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *source == "" {
		fs.Usage()
		return fmt.Errorf("-source is required outside go generate")
	}
	if *ifaceName == "" {
		inferred, err := inferInterfaceName(*source)
		if err != nil {
			return err
		}
		*ifaceName = inferred
	}

	iface, err := parseInterface(*source, *ifaceName)
	if err != nil {
		return err
	}

	resolved := make([]Backend, 0, len(backends))
	seenNames := make(map[string]struct{}, len(backends))
	for _, spec := range backends {
		name, importPath, err := parseBackendSpec(spec)
		if err != nil {
			return err
		}
		if _, exists := seenNames[name]; exists {
			return fmt.Errorf("duplicate backend name %q", name)
		}
		seenNames[name] = struct{}{}
		b, err := resolveBackend(name, importPath)
		if err != nil {
			return err
		}
		resolved = append(resolved, b)
	}

	view := buildView(iface, resolved)

	sourceBase := strings.TrimSuffix(filepath.Base(*source), ".go")
	dir := filepath.Dir(*source)
	existingTemplates := make(map[string]bool, len(view.Backends))
	for _, backend := range view.Backends {
		exists, methods, err := inspectTemplate(dir, backend.TemplateStructName)
		if err != nil {
			return err
		}
		if exists {
			existingTemplates[backend.Name] = true
			if missing := missingMethodNames(view.Methods, methods); len(missing) > 0 {
				return fmt.Errorf("backend %q template %s is missing methods: %s; add them to the existing implementation, then regenerate", backend.Name, backend.TemplateStructName, strings.Join(missing, ", "))
			}
			continue
		}
		stubPath := filepath.Join(dir, sourceBase+"_"+backend.Name+".go")
		if _, err := os.Stat(stubPath); err == nil {
			return fmt.Errorf("%s exists but does not declare %s; move it aside or add the expected template type", stubPath, backend.TemplateStructName)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat %s: %w", stubPath, err)
		}
	}

	outPath := *out
	if outPath == "" {
		outPath = filepath.Join(dir, sourceBase+"_gen.go")
	}
	genContent, err := renderGenFile(view)
	if err != nil {
		return fmt.Errorf("render %s: %w", outPath, err)
	}
	if err := os.WriteFile(outPath, genContent, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}
	fmt.Println("wrote", outPath)

	if *generateTest {
		testPath := filepath.Join(dir, sourceBase+"_gen_test.go")
		if err := writeIfMissing(testPath, func() ([]byte, error) { return renderTestSkeleton(view) }); err != nil {
			return err
		}
	}

	for _, b := range view.Backends {
		if existingTemplates[b.Name] {
			fmt.Println("skip (implemented)", b.TemplateStructName)
			continue
		}
		stubPath := filepath.Join(dir, sourceBase+"_"+b.Name+".go")
		bCopy := b
		if err := writeIfMissing(stubPath, func() ([]byte, error) { return renderBackendStub(view, bCopy) }); err != nil {
			return err
		}
	}

	return nil
}

var builtinAdapters = map[string]string{
	"sqlite":        "github.com/loykin/dbstore/adapters/sqlx",
	"mysql":         "github.com/loykin/dbstore/adapters/sqlx",
	"postgres":      "github.com/loykin/dbstore/adapters/sqlx",
	"sqlx":          "github.com/loykin/dbstore/adapters/sqlx",
	"rest":          "github.com/loykin/dbstore/adapters/rest",
	"opensearch":    "github.com/loykin/dbstore/adapters/opensearch",
	"elasticsearch": "github.com/loykin/dbstore/adapters/elasticsearch",
}

func parseBackendSpec(spec string) (name, importPath string, err error) {
	name, adapter, hasAdapter := strings.Cut(spec, ":")
	if name == "" {
		return "", "", fmt.Errorf("-backend %q: backend name is empty", spec)
	}
	if !hasAdapter {
		adapter = name
	}
	if adapter == "" {
		return "", "", fmt.Errorf("-backend %q: adapter is empty", spec)
	}
	if path, ok := builtinAdapters[adapter]; ok {
		adapter = path
	}
	return name, adapter, nil
}

// writeIfMissing renders and writes content only the first time — a
// generator that regenerated these files every run would either clobber
// hand-filled Template bodies or need risky file-merging logic. Once a
// backend gains a new interface method, the preflight check reports it before
// writing; the "var _ ... = XxxTemplate{}" assertion remains a compile-time
// guard for signature mismatches and manually generated glue.
func writeIfMissing(path string, render func() ([]byte, error)) error {
	if _, err := os.Stat(path); err == nil {
		fmt.Println("skip (exists)", path)
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	content, err := render()
	if err != nil {
		return fmt.Errorf("render %s: %w", path, err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	fmt.Println("wrote", path)
	return nil
}
