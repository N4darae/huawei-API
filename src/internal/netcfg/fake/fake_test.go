package fake

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

func TestMutatorsSucceed(t *testing.T) {
	m := New()
	ctx := context.Background()
	if err := m.EnsureGlobal(ctx, []netip.Addr{netip.MustParseAddr("139.99.68.39")}); err != nil {
		t.Fatalf("EnsureGlobal: %v", err)
	}
	if err := m.EnsureRouteTableNames(ctx); err != nil {
		t.Fatalf("EnsureRouteTableNames: %v", err)
	}
	if err := m.ApplySlot(ctx, domain.Slot(1), "pci-x", ""); err != nil {
		t.Fatalf("ApplySlot: %v", err)
	}
	if err := m.RemoveSlot(ctx, domain.Slot(1)); err != nil {
		t.Fatalf("RemoveSlot: %v", err)
	}
}

func TestProbesRefuseToInvent(t *testing.T) {
	m := New()
	ctx := context.Background()
	if _, err := m.Observe(ctx); !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("a fake observation would ship the farm believing rotation works; want ErrNotImplemented, got %v", err)
	}
	if _, _, err := m.Subscribe(ctx); !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("want ErrNotImplemented, got %v", err)
	}
}

func TestInvariantsFailLoudly(t *testing.T) {
	v := New().AssertInvariants(context.Background())
	if len(v) == 0 {
		t.Fatal("the fake must never report a healthy routing setup")
	}
	names := map[string]bool{}
	for _, x := range v {
		names[x.Name] = true
		if x.Detail != domain.ErrNotImplemented.Error() {
			t.Fatalf("violation %s must say it is not implemented, got %q", x.Name, x.Detail)
		}
	}
	for _, want := range []string{
		domain.InvariantPublicSrcRule,
		domain.InvariantCustomerLeg,
		domain.InvariantEgressFenced,
		domain.InvariantDNSContained,
	} {
		if !names[want] {
			t.Fatalf("invariant %s must be reported as unverified", want)
		}
	}
}
