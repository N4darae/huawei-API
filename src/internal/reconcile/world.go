package reconcile

import (
	"context"
	"net/netip"
	"time"

	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/netcfg"
	"github.com/n4darae/huawei-API/src/internal/proxysup"
	"github.com/n4darae/huawei-API/src/internal/store"
)

const RotationLookback = 24 * time.Hour

func OpKey(subject domain.SubjectType, id string) string {
	return string(subject) + ":" + id
}

func (d DesiredState) SlotRows() []domain.SlotRow {
	out := make([]domain.SlotRow, 0, len(d.Slots))
	for _, s := range sortedSlots(d.Slots) {
		out = append(out, d.Slots[s])
	}
	return out
}

func (d DesiredState) DongleFor(row domain.SlotRow) (domain.Dongle, bool) {
	if !row.Occupied() {
		return domain.Dongle{}, false
	}
	dg, ok := d.Dongles[*row.DongleID]
	return dg, ok
}

func (d DesiredState) ProxiesBySlot() map[string]domain.Proxy {
	out := make(map[string]domain.Proxy, len(d.Proxies))
	for _, p := range d.Proxies {
		if cur, ok := out[p.SlotID]; ok && cur.ID <= p.ID {
			continue
		}
		out[p.SlotID] = p
	}
	return out
}

func LoadDesired(ctx context.Context, repos store.Repos, nodeID string) (DesiredState, error) {
	out := DesiredState{
		Slots:   map[domain.Slot]domain.SlotRow{},
		Dongles: map[string]domain.Dongle{},
		Proxies: map[string]domain.Proxy{},
		AuthIPs: map[string][]netip.Prefix{},
	}

	node, err := repos.Nodes().Get(ctx, nodeID)
	if err != nil {
		return DesiredState{}, err
	}
	out.Node = node

	rows, err := repos.Slots().List(ctx, nodeID)
	if err != nil {
		return DesiredState{}, err
	}
	slotIDs := make(map[string]bool, len(rows))
	for _, r := range rows {
		if !r.Slot.Valid() {
			continue
		}
		out.Slots[r.Slot] = r
		slotIDs[r.ID] = true
	}

	dongles, err := repos.Dongles().List(ctx, store.DongleFilter{NodeID: nodeID})
	if err != nil {
		return DesiredState{}, err
	}
	for _, d := range dongles {
		out.Dongles[d.ID] = d
	}

	proxies, err := repos.Proxies().List(ctx, store.ProxyFilter{})
	if err != nil {
		return DesiredState{}, err
	}
	for _, p := range proxies {
		if !slotIDs[p.SlotID] {
			continue
		}
		out.Proxies[p.ID] = p
		if len(p.AuthIPs) > 0 {
			out.AuthIPs[p.ID] = p.AuthIPs
		}
	}
	return out, nil
}

func LoadActiveOps(ctx context.Context, repos store.Repos) (map[string]domain.Operation, error) {
	ops, err := repos.Operations().ListActive(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]domain.Operation, len(ops))
	for _, o := range ops {
		out[OpKey(o.SubjectType, o.SubjectID)] = o
	}
	return out, nil
}

type BudgetPolicy struct {
	RebootPerDay        int
	RebootCooldown      time.Duration
	MaxConcurrentRotate int
	MinRotateInterval   time.Duration
}

func LoadBudgets(ctx context.Context, repos store.Repos, policy BudgetPolicy, now time.Time, active map[string]domain.Operation) (Budgets, error) {
	b := Budgets{
		RebootPerDay:        policy.RebootPerDay,
		RebootCooldown:      policy.RebootCooldown,
		RebootUsed:          map[string]int{},
		LastRebootAt:        map[string]time.Time{},
		MaxConcurrentRotate: policy.MaxConcurrentRotate,
		MinRotateInterval:   policy.MinRotateInterval,
		LastRotateAt:        map[string]time.Time{},
	}

	dayStart := now.UTC().Truncate(24 * time.Hour)
	reboots, err := repos.Operations().List(ctx, store.OperationFilter{
		Kind:    domain.OpReboot,
		SinceMS: domain.UnixMillis(dayStart),
	})
	if err != nil {
		return Budgets{}, err
	}
	for _, o := range reboots {
		b.RebootUsed[o.SubjectID]++
		at := domain.FromUnixMillis(o.StartedAt)
		if prev, ok := b.LastRebootAt[o.SubjectID]; !ok || at.After(prev) {
			b.LastRebootAt[o.SubjectID] = at
		}
	}

	rotations, err := repos.Rotations().List(ctx, store.RotationFilter{
		SinceMS: domain.UnixMillis(now.Add(-RotationLookback)),
	})
	if err != nil {
		return Budgets{}, err
	}
	for _, r := range rotations {
		at := domain.FromUnixMillis(r.RequestedAt)
		if prev, ok := b.LastRotateAt[r.ProxyID]; !ok || at.After(prev) {
			b.LastRotateAt[r.ProxyID] = at
		}
	}

	for _, o := range active {
		if o.Kind == domain.OpRotate {
			b.RotateInFlight++
		}
	}
	return b, nil
}

func SpecFor(node domain.Node, row domain.SlotRow, px domain.Proxy, nserverFallback netip.Addr) proxysup.Spec {
	s := row.Slot
	nservers := []netip.Addr{s.GatewayIP()}
	if nserverFallback.IsValid() {
		nservers = append(nservers, nserverFallback)
	}
	ips := append([]netip.Prefix(nil), px.AuthIPs...)
	domain.SortPrefixes(ips)

	return proxysup.Spec{
		Slot:       s,
		InternalIP: node.PublicHost,
		ExternalIP: s.HostIP(),
		SocksPort:  px.SocksPort,
		HTTPPort:   px.HTTPPort,
		Users:      []proxysup.User{{Name: px.Username, Password: px.Password}},
		AuthMode:   px.AuthMode,
		AuthIPs:    ips,
		Policy:     px.Policy,
		NServers:   nservers,
		LogPath:    s.ProxyLogPath(),
		ConfigPath: s.ProxyConfigPath(),
		UID:        s.UID(),
		GID:        s.GID(),
	}
}

func (o ObservedState) Clone() ObservedState {
	out := o
	out.Net = cloneObservation(o.Net)
	out.Fenced = make(map[string]bool, len(o.Fenced))
	for k, v := range o.Fenced {
		out.Fenced[k] = v
	}
	out.ProxyStatus = make(map[domain.Slot]proxysup.Status, len(o.ProxyStatus))
	for k, v := range o.ProxyStatus {
		out.ProxyStatus[k] = v
	}
	out.Devices = make(map[domain.Slot]DeviceObservation, len(o.Devices))
	for k, v := range o.Devices {
		out.Devices[k] = v
	}
	return out
}

func cloneObservation(in netcfg.Observation) netcfg.Observation {
	out := in
	out.Links = make(map[string]netcfg.LinkState, len(in.Links))
	for k, v := range in.Links {
		v.Addrs = append([]netip.Prefix(nil), v.Addrs...)
		out.Links[k] = v
	}
	out.Rules = append([]netcfg.RuleState(nil), in.Rules...)
	out.Routes = make(map[int][]netcfg.RouteState, len(in.Routes))
	for k, v := range in.Routes {
		out.Routes[k] = append([]netcfg.RouteState(nil), v...)
	}
	out.DuplicateAddrs = append([]netip.Prefix(nil), in.DuplicateAddrs...)
	out.ForeignRuleBelowCeil = append([]netcfg.RuleState(nil), in.ForeignRuleBelowCeil...)
	out.PublicSrcRules = append([]netcfg.RuleState(nil), in.PublicSrcRules...)
	return out
}

func newObservedState() ObservedState {
	return ObservedState{
		Net: netcfg.Observation{
			Links:  map[string]netcfg.LinkState{},
			Routes: map[int][]netcfg.RouteState{},
		},
		Fenced:      map[string]bool{},
		ProxyStatus: map[domain.Slot]proxysup.Status{},
		Devices:     map[domain.Slot]DeviceObservation{},
	}
}
