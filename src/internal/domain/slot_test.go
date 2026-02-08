package domain

import (
	"net/netip"
	"testing"

	"github.com/n4darae/huawei-API/src/internal/config"
)

func TestSlotRulePrioritiesStayBelowForeignRules(t *testing.T) {
	last := Slot(MaxSlots)
	if got := last.RulePrioUID(); got >= ForeignRuleCeil {
		t.Fatalf("Slot(%d).RulePrioUID() = %d, must stay below %d (tailscale owns 5210+)", MaxSlots, got, ForeignRuleCeil)
	}
	if got := last.RulePrioSrc(); got >= ForeignRuleCeil {
		t.Fatalf("Slot(%d).RulePrioSrc() = %d, must stay below %d", MaxSlots, got, ForeignRuleCeil)
	}
	if RulePrioPublic >= RulePrioSrc {
		t.Fatalf("RulePrioPublic %d must be evaluated before per-slot source rules (%d)", RulePrioPublic, RulePrioSrc)
	}
	for _, s := range Slots() {
		if s.RulePrioSrc() >= s.RulePrioUID() {
			t.Fatalf("slot %s: src rule %d must be evaluated before uid rule %d", s, s.RulePrioSrc(), s.RulePrioUID())
		}
		if s.RulePrioSrc() <= RulePrioPublic {
			t.Fatalf("slot %s: src rule %d must not preempt the public leg rule %d", s, s.RulePrioSrc(), RulePrioPublic)
		}
	}
}

func TestSlotAllocatorIsInjective(t *testing.T) {
	ifaces := map[string]Slot{}
	users := map[string]Slot{}
	uids := map[int]Slot{}
	tables := map[int]Slot{}
	socks := map[int]Slot{}
	https := map[int]Slot{}
	hosts := map[netip.Addr]Slot{}
	prios := map[int]Slot{}

	for _, s := range Slots() {
		if !s.Valid() {
			t.Fatalf("slot %d reported invalid", int(s))
		}
		mustBeUnique(t, ifaces, s.IfaceName(), s)
		mustBeUnique(t, users, s.UserName(), s)
		mustBeUnique(t, uids, s.UID(), s)
		mustBeUnique(t, tables, s.RouteTable(), s)
		mustBeUnique(t, socks, s.SocksPort(), s)
		mustBeUnique(t, https, s.HTTPPort(), s)
		mustBeUnique(t, hosts, s.HostIP(), s)
		mustBeUnique(t, prios, s.RulePrioSrc(), s)
		mustBeUnique(t, prios, s.RulePrioUID(), s)

		if s.SocksPort() >= config.EphemeralPortMin || s.HTTPPort() >= config.EphemeralPortMin {
			t.Fatalf("slot %s ports land inside the ephemeral range", s)
		}
		if s.SocksPort() < config.ProxyPortLo || s.HTTPPort() > config.ProxyPortHi {
			t.Fatalf("slot %s ports fall outside the nft proxy_ports set", s)
		}
		if !s.Subnet().Contains(s.HostIP()) || !s.Subnet().Contains(s.GatewayIP()) {
			t.Fatalf("slot %s subnet %s does not contain its own addresses", s, s.Subnet())
		}
		if got, ok := ParseIfaceName(s.IfaceName()); !ok || got != s {
			t.Fatalf("ParseIfaceName(%q) = %v %v", s.IfaceName(), got, ok)
		}
		if got, ok := SlotFromUID(s.UID()); !ok || got != s {
			t.Fatalf("SlotFromUID(%d) = %v %v", s.UID(), got, ok)
		}
	}
}

func mustBeUnique[K comparable](t *testing.T, seen map[K]Slot, key K, s Slot) {
	t.Helper()
	if prev, ok := seen[key]; ok {
		t.Fatalf("slot %s collides with slot %s on %v", s, prev, key)
	}
	seen[key] = s
}

func TestSlotDerivedNames(t *testing.T) {
	s := Slot(1)
	cases := map[string]string{
		s.IfaceName():       "dg01",
		s.UserName():        "px01",
		s.LinkFileName():    "10-dongled-01.link",
		s.NetworkFileName(): "70-dongled-01.network",
		s.ProxyUnit():       "dongled-proxy@px01.service",
		s.Subnet().String(): "192.168.101.0/24",
		s.HostIP().String(): "192.168.101.100",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	if s.UID() != 6101 || s.RouteTable() != 1001 || s.SocksPort() != 21001 || s.HTTPPort() != 22001 {
		t.Errorf("slot 1 allocation drifted: uid=%d table=%d socks=%d http=%d", s.UID(), s.RouteTable(), s.SocksPort(), s.HTTPPort())
	}
	if s.RulePrioSrc() != 1001 || s.RulePrioUID() != 1501 {
		t.Errorf("slot 1 rule priorities drifted: src=%d uid=%d", s.RulePrioSrc(), s.RulePrioUID())
	}
}

func TestSlotRangeIsClosed(t *testing.T) {
	for _, s := range []Slot{-1, 0, MaxSlots + 1} {
		if s.Valid() {
			t.Errorf("slot %d must be invalid", int(s))
		}
	}
	if _, ok := ParseIfaceName("dg99"); ok {
		t.Error("ParseIfaceName accepted an out-of-range slot")
	}
	if _, ok := ParseIfaceName("enp1s0f0"); ok {
		t.Error("ParseIfaceName accepted a foreign interface")
	}
}
