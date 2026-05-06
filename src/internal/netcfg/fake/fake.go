package fake

import (
	"context"
	"net/netip"

	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/netcfg"
)

type Manager struct{}

var _ netcfg.Manager = (*Manager)(nil)

func New() *Manager { return &Manager{} }

func (m *Manager) EnsureGlobal(ctx context.Context, publicHosts []netip.Addr) error { return nil }

func (m *Manager) EnsureRouteTableNames(ctx context.Context) error { return nil }

func (m *Manager) ApplySlot(ctx context.Context, s domain.Slot, idPath, mac string) error { return nil }

func (m *Manager) RemoveSlot(ctx context.Context, s domain.Slot) error { return nil }

func (m *Manager) Observe(ctx context.Context) (netcfg.Observation, error) {
	return netcfg.Observation{}, domain.ErrNotImplemented
}

func (m *Manager) AssertInvariants(ctx context.Context) []netcfg.Violation {
	names := []string{
		domain.InvariantPublicSrcRule,
		domain.InvariantNoForeignRule,
		domain.InvariantCustomerLeg,
		domain.InvariantEgressFenced,
		domain.InvariantDNSContained,
		domain.InvariantRpFilterAll,
		domain.InvariantIPForward,
		domain.InvariantNoDuplicateAddrs,
	}
	out := make([]netcfg.Violation, 0, len(names))
	for _, n := range names {
		out = append(out, netcfg.Violation{Name: n, Detail: domain.ErrNotImplemented.Error()})
	}
	return out
}

func (m *Manager) Subscribe(ctx context.Context) (<-chan netcfg.LinkEvent, func(), error) {
	return nil, nil, domain.ErrNotImplemented
}
