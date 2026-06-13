package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

func evalVarFile(t *testing.T, file string) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	exprs := map[string]ast.Expr{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if i < len(vs.Values) {
					exprs[name.Name] = vs.Values[i]
				}
			}
		}
	}
	resolved := map[string]string{}
	var resolve func(name string) string
	resolve = func(name string) string {
		if v, ok := resolved[name]; ok {
			return v
		}
		e, ok := exprs[name]
		if !ok {
			t.Fatalf("%s: no such var in %s", name, file)
		}
		v := evalStringExpr(t, e, resolve)
		resolved[name] = v
		return v
	}
	out := map[string]string{}
	for name := range exprs {
		out[name] = resolve(name)
	}
	return out
}

func evalStringExpr(t *testing.T, e ast.Expr, resolve func(string) string) string {
	t.Helper()
	switch v := e.(type) {
	case *ast.BasicLit:
		s, err := strconv.Unquote(v.Value)
		if err != nil {
			t.Fatalf("unquote %s: %v", v.Value, err)
		}
		return s
	case *ast.Ident:
		if v.Name == "Product" {
			return Product
		}
		return resolve(v.Name)
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			t.Fatalf("unsupported operator %s", v.Op)
		}
		return evalStringExpr(t, v.X, resolve) + evalStringExpr(t, v.Y, resolve)
	}
	t.Fatalf("unsupported expression %T", e)
	return ""
}

func TestUnixBaseDirsMatchTheProductionLayout(t *testing.T) {
	want := map[string]string{
		"EtcDir":    "/etc/" + Product,
		"RunDir":    "/run/" + Product,
		"StateDir":  "/var/lib/" + Product,
		"LogDir":    "/var/log/" + Product,
		"BackupDir": "/var/backups/" + Product,
		"BinDir":    "/usr/local/lib/" + Product,
	}
	got := evalVarFile(t, "paths_unix.go")
	for name, want := range want {
		if got[name] != want {
			t.Errorf("%s = %q, want %q", name, got[name], want)
		}
	}
}
