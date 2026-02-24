package files

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/netcfg"
)

const observedIDPath = "pci-0000:00:14.0-usb-0:13.1:1.0"

func TestRenderLinkUsesObservedIDPath(t *testing.T) {
	got, err := RenderLink(domain.Slot(1), observedIDPath)
	if err != nil {
		t.Fatalf("RenderLink: %v", err)
	}
	want := "[Match]\nPath=" + observedIDPath + "\nType=ether\n\n[Link]\nName=dg01\nNamePolicy=\n"
	if string(got) != want {
		t.Fatalf("link file mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(string(got), "platform-") {
		t.Fatal("link Match must not carry a platform template literal")
	}
}

func TestRenderLinkRejectsEmptyIDPath(t *testing.T) {
	if _, err := RenderLink(domain.Slot(1), "  "); !errors.Is(err, netcfg.ErrNoIDPath) {
		t.Fatalf("want ErrNoIDPath, got %v", err)
	}
}

func TestRenderLinkRejectsInvalidSlot(t *testing.T) {
	if _, err := RenderLink(domain.Slot(0), observedIDPath); !errors.Is(err, netcfg.ErrInvalidSlot) {
		t.Fatalf("want ErrInvalidSlot, got %v", err)
	}
	if _, err := RenderNetwork(domain.Slot(domain.MaxSlots + 1)); !errors.Is(err, netcfg.ErrInvalidSlot) {
		t.Fatalf("want ErrInvalidSlot, got %v", err)
	}
}

func TestRenderNetworkGolden(t *testing.T) {
	got, err := RenderNetwork(domain.Slot(1))
	if err != nil {
		t.Fatalf("RenderNetwork: %v", err)
	}
	want := strings.Join([]string{
		"[Match]",
		"Name=dg01",
		"",
		"[Link]",
		"RequiredForOnline=no",
		"",
		"[Network]",
		"Address=192.168.101.100/24",
		"DHCP=no",
		"IPv6AcceptRA=no",
		"LinkLocalAddressing=no",
		"ConfigureWithoutCarrier=yes",
		"IgnoreCarrierLoss=yes",
		"",
		"[Route]",
		"Destination=0.0.0.0/0",
		"Gateway=192.168.101.1",
		"Table=1001",
		"",
		"[Route]",
		"Destination=192.168.101.0/24",
		"Scope=link",
		"Table=1001",
		"",
		"[RoutingPolicyRule]",
		"From=192.168.101.100/32",
		"Table=1001",
		"Priority=1001",
		"",
		"[RoutingPolicyRule]",
		"User=6101-6101",
		"Table=1001",
		"Priority=1501",
		"",
	}, "\n")
	if string(got) != want {
		t.Fatalf("network file mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderNetworkHasExactlyTwoRoutingPolicyRules(t *testing.T) {
	for _, s := range domain.Slots() {
		body, err := RenderNetwork(s)
		if err != nil {
			t.Fatalf("slot %s: %v", s, err)
		}
		if n := strings.Count(string(body), "[RoutingPolicyRule]"); n != 2 {
			t.Fatalf("slot %s has %d routing policy rules, want 2", s, n)
		}
		if strings.Contains(string(body), "OutgoingInterface") {
			t.Fatalf("slot %s still carries the oif rule that was measured to match zero packets", s)
		}
		if strings.Contains(string(body), "DHCP=yes") {
			t.Fatalf("slot %s must not enable dhcp", s)
		}
	}
}

func TestRulePrioritiesStayInTheMeasuredOrder(t *testing.T) {
	for _, s := range domain.Slots() {
		if !(domain.RulePrioPublic < s.RulePrioSrc()) {
			t.Fatalf("slot %s: public rule %d must sort before src rule %d", s, domain.RulePrioPublic, s.RulePrioSrc())
		}
		if !(s.RulePrioSrc() < s.RulePrioUID()) {
			t.Fatalf("slot %s: src rule %d must sort before uid rule %d", s, s.RulePrioSrc(), s.RulePrioUID())
		}
		if !(s.RulePrioUID() < domain.ForeignRuleCeil) {
			t.Fatalf("slot %s: uid rule %d must stay below the foreign ceiling %d", s, s.RulePrioUID(), domain.ForeignRuleCeil)
		}
	}
}

func TestWriteSlotIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	r := NewRenderer(dir)
	first, err := r.WriteSlot(domain.Slot(3), observedIDPath)
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	if !first.Link || !first.Network {
		t.Fatalf("first write should report both files changed, got %+v", first)
	}
	second, err := r.WriteSlot(domain.Slot(3), observedIDPath)
	if err != nil {
		t.Fatalf("second write: %v", err)
	}
	if second.Any() {
		t.Fatalf("second write should be a no-op, got %+v", second)
	}
	third, err := r.WriteSlot(domain.Slot(3), "pci-0000:00:14.0-usb-0:2.4:1.0")
	if err != nil {
		t.Fatalf("third write: %v", err)
	}
	if !third.Link || third.Network {
		t.Fatalf("changing only the id path should rewrite only the link file, got %+v", third)
	}
}

func TestWriteSlotFileNamesAndMode(t *testing.T) {
	dir := t.TempDir()
	r := NewRenderer(dir)
	if _, err := r.WriteSlot(domain.Slot(7), observedIDPath); err != nil {
		t.Fatalf("write: %v", err)
	}
	for _, name := range []string{"10-dongled-07.link", "70-dongled-07.network"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if info.Mode().Perm() != 0o644 {
			t.Fatalf("%s mode is %v, want 0644", name, info.Mode().Perm())
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Fatalf("temporary file %s was left behind", e.Name())
		}
	}
}

func TestRemoveSlotOnMissingFilesIsNoOp(t *testing.T) {
	r := NewRenderer(t.TempDir())
	changed, err := r.RemoveSlot(domain.Slot(9))
	if err != nil {
		t.Fatalf("remove on empty dir: %v", err)
	}
	if changed.Any() {
		t.Fatalf("remove on empty dir should report nothing changed, got %+v", changed)
	}
}

func TestRouteTablesRender(t *testing.T) {
	got := string(RenderRouteTables([]domain.Slot{domain.Slot(2), domain.Slot(1)}))
	want := "1001\tdongled01\n1002\tdongled02\n"
	if got != want {
		t.Fatalf("route tables mismatch:\ngot:\n%q\nwant:\n%q", got, want)
	}
	path := filepath.Join(t.TempDir(), "dongled.conf")
	if ok, err := WriteRouteTables(path, domain.Slots()); err != nil || !ok {
		t.Fatalf("write route tables: ok=%v err=%v", ok, err)
	}
	if !RouteTablesComplete(path, domain.Slots()) {
		t.Fatal("route tables should be reported complete after writing")
	}
	if ok, err := WriteRouteTables(path, domain.Slots()); err != nil || ok {
		t.Fatalf("second write should be a no-op: ok=%v err=%v", ok, err)
	}
}
