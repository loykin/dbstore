// Command dbstore-gen mirrors a hand-written domain interface into the
// generic wiring a multi-backend repository needs (Template[A] interface +
// generic wrapper), and scaffolds a compliance-test skeleton and one
// Template stub per backend the first time they're needed. See
// docs/design-codegen.md for the design this narrow generator implements —
// it deliberately does not do anything the type system or dbstoretest
// already handles (see that document's "안전장치는 타입 시스템에" principle).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// backendFlags collects repeated -backend name:importpath flags.
type backendFlags []string

func (b *backendFlags) String() string { return strings.Join(*b, ",") }
func (b *backendFlags) Set(v string) error {
	*b = append(*b, v)
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "dbstore-gen:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("dbstore-gen", flag.ContinueOnError)
	ifaceName := fs.String("interface", "", "domain interface name to mirror (required)")
	source := fs.String("source", "", "Go file declaring -interface (required)")
	out := fs.String("out", "", "output file for the generated glue (default: <source base>_gen.go)")
	var backends backendFlags
	fs.Var(&backends, "backend", "name:importpath, repeatable — the importpath's package must export an Adaptor type")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *ifaceName == "" || *source == "" {
		fs.Usage()
		return fmt.Errorf("-interface and -source are required")
	}

	iface, err := parseInterface(*source, *ifaceName)
	if err != nil {
		return err
	}

	resolved := make([]Backend, 0, len(backends))
	for _, spec := range backends {
		name, importPath, ok := strings.Cut(spec, ":")
		if !ok {
			return fmt.Errorf("-backend %q: want name:importpath", spec)
		}
		b, err := resolveBackend(name, importPath)
		if err != nil {
			return err
		}
		resolved = append(resolved, b)
	}

	view := buildView(iface, resolved)

	sourceBase := strings.TrimSuffix(filepath.Base(*source), ".go")
	dir := filepath.Dir(*source)

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

	testPath := filepath.Join(dir, sourceBase+"_gen_test.go")
	if err := writeIfMissing(testPath, func() ([]byte, error) { return renderTestSkeleton(view) }); err != nil {
		return err
	}

	for _, b := range view.Backends {
		stubPath := filepath.Join(dir, sourceBase+"_"+b.Name+".go")
		bCopy := b
		if err := writeIfMissing(stubPath, func() ([]byte, error) { return renderBackendStub(view, bCopy) }); err != nil {
			return err
		}
	}

	return nil
}

// writeIfMissing renders and writes content only the first time — a
// generator that regenerated these files every run would either clobber
// hand-filled Template bodies or need risky file-merging logic. Once a
// backend gains a new interface method, the "var _ ... = XxxTemplate{}"
// assertion in the _gen.go file (always rewritten) surfaces it as a
// compile error, and the missing method is added by hand — see
// docs/design-codegen.md's "이런게 실제로 문제가 될 수 있는 거 아냐" note.
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
