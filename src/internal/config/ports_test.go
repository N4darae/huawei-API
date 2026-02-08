package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/netip"
	"strconv"
	"strings"
	"testing"
)

func TestEveryPortLiteralIsBelowEphemeralRange(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "ports.go", nil, 0)
	if err != nil {
		t.Fatalf("parse ports.go: %v", err)
	}

	seen := 0
	check := func(pos token.Pos, name string, port int) {
		seen++
		if port >= EphemeralPortMin {
			t.Errorf("%s: port %d in %s collides with the ephemeral range %d-%d",
				fset.Position(pos), port, name, EphemeralPortMin, EphemeralPortMax)
		}
	}

	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok {
			return true
		}
		switch lit.Kind {
		case token.INT:
			v, err := strconv.Atoi(lit.Value)
			if err != nil || v < 1024 || v > 65535 {
				return true
			}
			check(lit.Pos(), lit.Value, v)
		case token.STRING:
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			i := strings.LastIndex(s, ":")
			if i < 0 {
				return true
			}
			v, err := strconv.Atoi(s[i+1:])
			if err != nil {
				return true
			}
			check(lit.Pos(), s, v)
		}
		return true
	})

	if seen < 8 {
		t.Fatalf("only %d port literals inspected in ports.go; the scan is not covering the file", seen)
	}
}

func TestListenAddressesResolve(t *testing.T) {
	for _, addr := range []string{PanelAddr, MetricsAddr} {
		ap, err := netip.ParseAddrPort(addr)
		if err != nil {
			t.Fatalf("%s: %v", addr, err)
		}
		if !ap.Addr().IsLoopback() {
			t.Errorf("%s must stay on loopback; public exposure goes through nginx", addr)
		}
		if int(ap.Port()) >= EphemeralPortMin {
			t.Errorf("%s is inside the ephemeral range", addr)
		}
	}
	if PanelAddr != PanelHost+":"+strconv.Itoa(PanelPort) {
		t.Errorf("PanelAddr %q disagrees with PanelHost/PanelPort", PanelAddr)
	}
	if MetricsAddr != MetricsHost+":"+strconv.Itoa(MetricsPort) {
		t.Errorf("MetricsAddr %q disagrees with MetricsHost/MetricsPort", MetricsAddr)
	}
}

func TestProxyPortRangeCoversSlotPorts(t *testing.T) {
	if ProxyPortLo != SocksPortBase {
		t.Errorf("ProxyPortLo %d must equal SocksPortBase %d", ProxyPortLo, SocksPortBase)
	}
	if ProxyPortHi < HTTPPortBase {
		t.Errorf("ProxyPortHi %d does not cover HTTPPortBase %d", ProxyPortHi, HTTPPortBase)
	}
	if ProxyValidatePort >= ProxyPortLo && ProxyValidatePort <= ProxyPortHi {
		t.Errorf("ProxyValidatePort %d overlaps the live proxy port range", ProxyValidatePort)
	}
	if ViteDevPort == 5173 {
		t.Error("ViteDevPort 5173 is occupied on the dev host")
	}
}

func TestValidateRejectsNonPublicHost(t *testing.T) {
	cases := []string{"127.0.0.1", "10.0.0.1", "192.168.1.1", "100.64.0.1", "0.0.0.0", "224.0.0.1"}
	for _, s := range cases {
		c := Default()
		c.PublicHost = netip.MustParseAddr(s)
		if err := c.Validate(); err == nil {
			t.Errorf("Validate accepted non-public host %s", s)
		}
	}
	c := Default()
	c.PublicHost = netip.MustParseAddr("139.99.68.39")
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate rejected a valid config: %v", err)
	}
}
