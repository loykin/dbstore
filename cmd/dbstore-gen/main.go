// Command dbstore-gen mirrors a hand-written domain interface into the
// generic wiring a multi-backend repository needs (RepoBackend[A] interface +
// generic wrapper), scaffolds app-owned Backend/compliance/fixture files the
// first time they are needed, and regenerates the configured fixture registry.
// It deliberately leaves structural safety to the Go type system and
// behavioral portability to dbstoretest instead of duplicating either in the
// generator.
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
	configPath := fs.String("config", "", "YAML file declaring interface/source/backends; mutually exclusive with -interface/-source/-backend")
	ifaceName := fs.String("interface", "", "domain interface name to mirror (required unless -config is used — never inferred)")
	source := fs.String("source", os.Getenv("GOFILE"), "Go file declaring -interface (defaults to GOFILE under go generate)")
	out := fs.String("out", "", "output file for the generated glue (default: <source base>_gen.go)")
	generateTest := fs.Bool("test", false, "scaffold app-owned compliance files and generate the backend fixture registry")
	var backends backendFlags
	fs.Var(&backends, "backend", "name[:adapter], repeatable; built-ins: sqlite, mysql, postgres, sqlx, rest, opensearch, elasticsearch")

	if err := fs.Parse(args); err != nil {
		return err
	}

	effectiveIface := *ifaceName
	effectiveSource := *source
	effectiveTest := *generateTest
	backendSpecs := []string(backends)

	if *configPath != "" {
		if *ifaceName != "" || len(backends) > 0 {
			return fmt.Errorf("-config cannot be combined with -interface or -backend; put them in %s instead", *configPath)
		}
		cfg, err := loadConfig(*configPath)
		if err != nil {
			return err
		}
		effectiveIface = cfg.Interface
		effectiveSource = cfg.Source
		effectiveTest = cfg.Test || *generateTest
		backendSpecs = make([]string, len(cfg.Backends))
		for i, b := range cfg.Backends {
			backendSpecs[i] = b.Name + ":" + b.Adapter
		}
	} else if effectiveIface == "" {
		fs.Usage()
		return fmt.Errorf("-interface is required (or use -config) — dbstore-gen never guesses which interface to mirror")
	}
	if effectiveSource == "" {
		fs.Usage()
		return fmt.Errorf("-source is required outside go generate (or use -config)")
	}

	iface, err := parseInterface(effectiveSource, effectiveIface)
	if err != nil {
		return err
	}

	resolved := make([]Backend, 0, len(backendSpecs))
	seenNames := make(map[string]struct{}, len(backendSpecs))
	for _, spec := range backendSpecs {
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

	sourceBase := strings.TrimSuffix(filepath.Base(effectiveSource), ".go")
	dir := filepath.Dir(effectiveSource)
	existingBackends := make(map[string]bool, len(view.Backends))
	for _, backend := range view.Backends {
		exists, methods, err := inspectBackend(dir, backend.BackendStructName)
		if err != nil {
			return err
		}
		if exists {
			existingBackends[backend.Name] = true
			if missing := missingMethods(view.Methods, methods); len(missing) > 0 {
				names := make([]string, len(missing))
				for i, method := range missing {
					names[i] = method.Name
				}
				return fmt.Errorf("backend %q implementation %s is missing methods: %s\n\nadd these methods to the existing implementation, then regenerate:\n\n%s", backend.Name, backend.BackendStructName, strings.Join(names, ", "), renderMissingMethodStubs(backend, missing))
			}
			continue
		}
		stubPath := filepath.Join(dir, sourceBase+"_"+backend.Name+".go")
		if _, err := os.Stat(stubPath); err == nil {
			return fmt.Errorf("%s exists but does not declare %s; move it aside or add the expected backend type", stubPath, backend.BackendStructName)
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

	if effectiveTest {
		suitePath := filepath.Join(dir, sourceBase+"_compliance_test.go")
		if err := writeIfMissing(suitePath, func() ([]byte, error) { return renderComplianceSuiteSkeleton(view) }); err != nil {
			return err
		}
		for _, backend := range view.Backends {
			fixturePath := filepath.Join(dir, sourceBase+"_"+backend.Name+"_test.go")
			backendCopy := backend
			if err := writeIfMissing(fixturePath, func() ([]byte, error) { return renderFixtureStub(view, backendCopy) }); err != nil {
				return err
			}
		}
		registryPath := filepath.Join(dir, sourceBase+"_compliance_gen_test.go")
		registryContent, err := renderComplianceRegistry(view)
		if err != nil {
			return fmt.Errorf("render %s: %w", registryPath, err)
		}
		if err := os.WriteFile(registryPath, registryContent, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", registryPath, err)
		}
		fmt.Println("wrote", registryPath)
	}

	for _, b := range view.Backends {
		if existingBackends[b.Name] {
			fmt.Println("skip (implemented)", b.BackendStructName)
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
// hand-filled Backend bodies or need risky file-merging logic. Once a
// backend gains a new interface method, the preflight check reports it before
// writing; the "var _ ... = XxxBackend{}" assertion remains a compile-time
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
