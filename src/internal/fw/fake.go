package fw

import (
	"context"
	"net/netip"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

type Fake struct{}

var _ Firewall = (*Fake)(nil)

func NewFake() *Fake { return &Fake{} }

func (f *Fake) EnsureTable(ctx context.Context) error { return nil }

func (f *Fake) AddPublic(ctx context.Context, iface string, ip netip.Addr) error { return nil }

func (f *Fake) RemovePublic(ctx context.Context, iface string, ip netip.Addr) error { return nil }

func (f *Fake) AddDongle(ctx context.Context, iface string, gw netip.Addr) error { return nil }

func (f *Fake) RemoveDongle(ctx context.Context, iface string) error { return nil }

func (f *Fake) Fence(ctx context.Context, iface string) error { return nil }

func (f *Fake) Unfence(ctx context.Context, iface string) error { return nil }

func (f *Fake) Verify(ctx context.Context) error { return domain.ErrNotImplemented }

func (f *Fake) IsFenced(ctx context.Context, iface string) (bool, error) {
	return false, domain.ErrNotImplemented
}

func (f *Fake) KillSockets(ctx context.Context, src netip.Addr) (int, error) {
	return 0, domain.ErrNotImplemented
}

func (f *Fake) FlushConntrack(ctx context.Context, src netip.Addr) (int, error) {
	return 0, domain.ErrNotImplemented
}

func (f *Fake) CustomerAcceptHits(ctx context.Context) (uint64, error) {
	return 0, domain.ErrNotImplemented
}
