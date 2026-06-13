package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"runtime"
	"strconv"
	"strings"
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

func TestLinuxOnlyHostNetPathsMatchTheProductionLayout(t *testing.T) {
	want := map[string]string{
		"NetworkDir":   "/etc/systemd/network",
		"RtTablesDir":  "/etc/iproute2/rt_tables.d",
		"RtTablesFile": "/etc/iproute2/rt_tables.d/" + Product + ".conf",
	}
	got := evalVarFile(t, "hostnet_linux.go")
	for name, want := range want {
		if got[name] != want {
			t.Errorf("%s = %q, want %q", name, got[name], want)
		}
	}
}

func TestDerivedPathsFollowTheirBaseDirs(t *testing.T) {
	if ProxyConfDir != EtcDir+"/proxy" {
		t.Errorf("ProxyConfDir = %q, want %q", ProxyConfDir, EtcDir+"/proxy")
	}
	if DBPath != StateDir+"/"+Product+".db" {
		t.Errorf("DBPath = %q, want %q", DBPath, StateDir+"/"+Product+".db")
	}
	if Bin3proxy != BinDir+"/3proxy" {
		t.Errorf("Bin3proxy = %q, want %q", Bin3proxy, BinDir+"/3proxy")
	}
	if FarmMarker != EtcDir+"/FARM" {
		t.Errorf("FarmMarker = %q, want %q", FarmMarker, EtcDir+"/FARM")
	}
	if KEKCredFile != EtcDir+"/kek.cred" {
		t.Errorf("KEKCredFile = %q, want %q", KEKCredFile, EtcDir+"/kek.cred")
	}
	user := "px01"
	if got, want := ProxyConfigPath(user), ProxyConfDir+"/"+user+".cfg"; got != want {
		t.Errorf("ProxyConfigPath(%q) = %q, want %q", user, got, want)
	}
	if got, want := ProxyLogPath(user), LogDir+"/"+user+".log"; got != want {
		t.Errorf("ProxyLogPath(%q) = %q, want %q", user, got, want)
	}
}

func TestWindowsBaseDirsUseProgramData(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("this assertion only applies to the windows layout")
	}
	base := os.Getenv("ProgramData")
	if base == "" {
		base = "C:/ProgramData"
	}
	base = strings.ReplaceAll(base, "\\", "/") + "/" + Product
	want := map[string]string{
		"EtcDir":    base + "/etc",
		"RunDir":    base + "/run",
		"StateDir":  base + "/state",
		"LogDir":    base + "/log",
		"BackupDir": base + "/backup",
		"BinDir":    base + "/lib",
	}
	got := map[string]string{
		"EtcDir":    EtcDir,
		"RunDir":    RunDir,
		"StateDir":  StateDir,
		"LogDir":    LogDir,
		"BackupDir": BackupDir,
		"BinDir":    BinDir,
	}
	for name, want := range want {
		if got[name] != want {
			t.Errorf("%s = %q, want %q", name, got[name], want)
		}
	}
	if NetworkDir != "" || RtTablesDir != "" || RtTablesFile != "" {
		t.Errorf("linux-only paths must stay empty on windows: NetworkDir=%q RtTablesDir=%q RtTablesFile=%q", NetworkDir, RtTablesDir, RtTablesFile)
	}
}
