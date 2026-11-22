package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Switching to the real Bacen endpoints must be a change of configuration, so nothing but this package may know where the
// mocks are, and nothing may depend on their code.

var mockISPB = regexp.MustCompile(`\b9900000\d\b`)

func sources(t *testing.T) map[string]*ast.File {
	t.Helper()
	root := filepath.Join("..", "..", "..")
	fset := token.NewFileSet()
	out := map[string]*ast.File{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		switch {
		case d.IsDir() && (rel == "e2e" || rel == "gen" || rel == "migrations"):
			return filepath.SkipDir
		case d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go"):
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = f
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < 10 {
		t.Fatalf("found only %d source files: is the walk wrong?", len(out))
	}
	return out
}

func TestTheWalletDoesNotImportTheMocks(t *testing.T) {
	for name, f := range sources(t) {
		for _, imp := range f.Imports {
			if strings.Contains(imp.Path.Value, "pixfake/mock") {
				t.Errorf("%s imports %s", name, imp.Path.Value)
			}
		}
	}
	mod, err := os.ReadFile(filepath.Join("..", "..", "..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mod), "pixfake/mock") {
		t.Error("go.mod requires a mock")
	}
}

// Only the configuration and the seed (fake customers, which the real thing does not have) may name a place or a participant.
func TestNoEndpointOrParticipantIsHardCoded(t *testing.T) {
	allowed := func(name string) bool {
		return name == "internal/platform/config/config.go" || strings.HasPrefix(name, "internal/seed/")
	}
	for name, f := range sources(t) {
		if allowed(name) {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			switch {
			case strings.Contains(s, "/mock/"), strings.Contains(s, "localhost"), strings.Contains(s, "127.0.0.1"):
				t.Errorf("%s: %q names a mock or a local address", name, s)
			case strings.HasPrefix(s, "http://"), strings.HasPrefix(s, "https://") && !strings.HasPrefix(s, "https://www.bcb.gov.br/"):
				t.Errorf("%s: %q is a hard-coded URL", name, s)
			case mockISPB.MatchString(s):
				t.Errorf("%s: %q is the ISPB of a simulated participant", name, s)
			}
			return true
		})
	}
}
