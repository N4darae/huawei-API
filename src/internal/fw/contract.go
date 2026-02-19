package fw

import (
	"context"
	"net/netip"
)

const (
	ChainOutput = "output"
	ChainEgress = "proxy_egress"

	SetDongleIfaces = "dongle_ifaces"
	SetFencedIfaces = "fenced_ifaces"
	SetDongleGws    = "dongle_gws"
	SetPublicIfaces = "public_ifaces"
	SetPublicIPs    = "public_ips"
	SetProxyPorts   = "proxy_ports"
	SetBlackhole4   = "blackhole4"

	CommentCustomerLeg = "customer-facing leg"
	LogPrefixSSRF      = "dongled-ssrf "
	LogPrefixLeak      = "dongled-leak "
)

func AllSets() []string {
	return []string{
		SetDongleIfaces,
		SetFencedIfaces,
		SetDongleGws,
		SetPublicIfaces,
		SetPublicIPs,
		SetProxyPorts,
		SetBlackhole4,
	}
}

type Firewall interface {
	EnsureTable(ctx context.Context) error
	Verify(ctx context.Context) error
	AddPublic(ctx context.Context, iface string, ip netip.Addr) error
	RemovePublic(ctx context.Context, iface string, ip netip.Addr) error
	AddDongle(ctx context.Context, iface string, gw netip.Addr) error
	RemoveDongle(ctx context.Context, iface string) error
	Fence(ctx context.Context, iface string) error
	Unfence(ctx context.Context, iface string) error
	IsFenced(ctx context.Context, iface string) (bool, error)
	KillSockets(ctx context.Context, src netip.Addr) (int, error)
	FlushConntrack(ctx context.Context, src netip.Addr) (int, error)
	CustomerAcceptHits(ctx context.Context) (uint64, error)
}
