package netcfg

import (
	"context"
	"net/netip"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

const (
	OperStateUp      = "up"
	OperStateUnknown = "unknown"
	OperStateDown    = "down"
)

const (
	RequiredRpFilterAll = 2
	RequiredIPForward   = false
)

type LinkState struct {
	Name      string
	MAC       string
	IDPath    string
	Index     int
	MTU       int
	OperState string
	Carrier   bool
	Addrs     []netip.Prefix
}

func (l LinkState) Up() bool {
	return l.OperState == OperStateUp || l.OperState == OperStateUnknown
}

type RuleState struct {
	Priority   int
	Table      int
	Src        netip.Prefix
	Dst        netip.Prefix
	IifName    string
	UIDRangeLo int
	UIDRangeHi int
	Action     string
	Raw        string
}

type RouteState struct {
	Dst   netip.Prefix
	Gw    netip.Addr
	Dev   string
	Table int
	Scope string
	Src   netip.Addr
}

type Observation struct {
	Links                map[string]LinkState
	Rules                []RuleState
	Routes               map[int][]RouteState
	DuplicateAddrs       []netip.Prefix
	RpFilterAll          int
	IPForward            bool
	ForeignRuleBelowCeil []RuleState
	PublicSrcRules       []RuleState
	RouteTableNamesOK    bool
}

type LinkEventKind string

const (
	LinkAdded   LinkEventKind = "add"
	LinkDeleted LinkEventKind = "del"
	LinkChanged LinkEventKind = "change"
)

type LinkEvent struct {
	Kind LinkEventKind
	Link LinkState
	TS   int64
}

type Violation struct {
	Name   string
	Detail string
}

type Manager interface {
	EnsureGlobal(ctx context.Context, publicHosts []netip.Addr) error
	EnsureRouteTableNames(ctx context.Context) error
	ApplySlot(ctx context.Context, s domain.Slot, idPath, mac string) error
	RemoveSlot(ctx context.Context, s domain.Slot) error
	Observe(ctx context.Context) (Observation, error)
	AssertInvariants(ctx context.Context) []Violation
	Subscribe(ctx context.Context) (<-chan LinkEvent, func(), error)
}
