package reconcile

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

const pureFile = "plan.go"

var forbiddenImports = map[string]string{
	"net":              "Plan must not reach the network",
	"net/http":         "Plan must not reach the network",
	"os":               "Plan must not touch the filesystem or the environment",
	"os/exec":          "Plan must not shell out",
	"math/rand":        "Plan must be deterministic",
	"math/rand/v2":     "Plan must be deterministic",
	"crypto/rand":      "Plan must be deterministic",
	"database/sql":     "Plan must not query the database; everything it needs arrives in World",
	"context":          "Plan takes no context because it performs no I/O",
	"log":              "Plan must not log",
	"log/slog":         "Plan must not log",
	"syscall":          "Plan must not make system calls",
	"golang.org/x/sys": "Plan must not make system calls",
}

var forbiddenCalls = map[string]string{
	"time.Now":          "Plan must read the clock from World.Now",
	"time.Since":        "Plan must read the clock from World.Now",
	"time.Until":        "Plan must read the clock from World.Now",
	"time.Tick":         "Plan must not sleep or tick",
	"time.Sleep":        "Plan must not sleep or tick",
	"time.After":        "Plan must not sleep or tick",
	"rand.Float64":      "Plan must be deterministic",
	"rand.Intn":         "Plan must be deterministic",
	"rand.Int":          "Plan must be deterministic",
	"rand.N":            "Plan must be deterministic",
	"os.Getenv":         "Plan must not read the environment",
	"os.ReadFile":       "Plan must not touch the filesystem",
	"exec.Command":      "Plan must not shell out",
	"http.Get":          "Plan must not reach the network",
	"netip.ParseAddr":   "Plan must not parse strings that could fail; World carries typed values",
	"netip.ParsePrefix": "Plan must not parse strings that could fail; World carries typed values",
}

func parsePlan(t *testing.T) (*token.FileSet, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, pureFile, nil, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse %s: %v", pureFile, err)
	}
	return fset, f
}

func TestPlanImportsNothingImpure(t *testing.T) {
	fset, f := parsePlan(t)

	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			t.Fatalf("import path %s: %v", imp.Path.Value, err)
		}
		if why, banned := forbiddenImports[path]; banned {
			t.Errorf("%s imports %q: %s", fset.Position(imp.Pos()), path, why)
		}
		for prefix, why := range forbiddenImports {
			if strings.HasPrefix(path, prefix+"/") && path != "net/netip" {
				t.Errorf("%s imports %q: %s", fset.Position(imp.Pos()), path, why)
			}
		}
	}
}

func TestPlanCallsNothingImpure(t *testing.T) {
	fset, f := parsePlan(t)

	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		name := pkg.Name + "." + sel.Sel.Name
		if why, banned := forbiddenCalls[name]; banned {
			t.Errorf("%s calls %s: %s", fset.Position(sel.Pos()), name, why)
		}
		return true
	})
}

func TestPlanDeclaresNoPackageState(t *testing.T) {
	fset, f := parsePlan(t)

	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		t.Errorf("%s declares a package level var; Plan must be a function of its argument alone",
			fset.Position(gen.Pos()))
	}
}

func TestPlanIsDeterministic(t *testing.T) {
	w := farmWorld(t, farmOptions{slots: 48, healthy: true})

	first := Actions(Plan(w))
	for i := 0; i < 20; i++ {
		got := Actions(Plan(w))
		if len(got) != len(first) {
			t.Fatalf("run %d produced %d actions, first run produced %d", i, len(got), len(first))
		}
		for j := range got {
			if got[j].Kind() != first[j].Kind() || got[j].Target() != first[j].Target() {
				t.Fatalf("run %d differs at index %d: %s on %s vs %s on %s",
					i, j, got[j].Kind(), got[j].Target(), first[j].Kind(), first[j].Target())
			}
		}
	}
}
