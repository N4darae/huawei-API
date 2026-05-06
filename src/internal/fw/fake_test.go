package fw

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

func TestFakeMutatorsSucceed(t *testing.T) {
	f := NewFake()
	ctx := context.Background()
	ip := netip.MustParseAddr("139.99.68.39")
	gw := netip.MustParseAddr("192.168.101.1")
	if err := f.EnsureTable(ctx); err != nil {
		t.Fatalf("EnsureTable: %v", err)
	}
	if err := f.AddPublic(ctx, "enp1s0f0", ip); err != nil {
		t.Fatalf("AddPublic: %v", err)
	}
	if err := f.RemovePublic(ctx, "enp1s0f0", ip); err != nil {
		t.Fatalf("RemovePublic: %v", err)
	}
	if err := f.AddDongle(ctx, "dg01", gw); err != nil {
		t.Fatalf("AddDongle: %v", err)
	}
	if err := f.Fence(ctx, "dg01"); err != nil {
		t.Fatalf("Fence: %v", err)
	}
	if err := f.Unfence(ctx, "dg01"); err != nil {
		t.Fatalf("Unfence: %v", err)
	}
	if err := f.RemoveDongle(ctx, "dg01"); err != nil {
		t.Fatalf("RemoveDongle: %v", err)
	}
}

func TestFakeProbesRefuseToInvent(t *testing.T) {
	f := NewFake()
	ctx := context.Background()
	src := netip.MustParseAddr("192.168.101.100")
	if err := f.Verify(ctx); !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("Verify: want ErrNotImplemented, got %v", err)
	}
	if _, err := f.IsFenced(ctx, "dg01"); !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("IsFenced: want ErrNotImplemented, got %v", err)
	}
	if n, err := f.KillSockets(ctx, src); !errors.Is(err, domain.ErrNotImplemented) || n != 0 {
		t.Fatalf("a fake kill count would hide a fence that matched nothing; got %d %v", n, err)
	}
	if n, err := f.FlushConntrack(ctx, src); !errors.Is(err, domain.ErrNotImplemented) || n != 0 {
		t.Fatalf("FlushConntrack: got %d %v", n, err)
	}
	if n, err := f.CustomerAcceptHits(ctx); !errors.Is(err, domain.ErrNotImplemented) || n != 0 {
		t.Fatalf("CustomerAcceptHits: got %d %v", n, err)
	}
}
