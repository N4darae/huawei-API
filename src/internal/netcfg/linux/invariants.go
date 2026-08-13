package linux

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strconv"

	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/netcfg"
)

func (m *Manager) AssertInvariants(ctx context.Context) []netcfg.Violation {
	obs, err := m.Observe(ctx)
	if err != nil {
		return []netcfg.Violation{{Name: domain.InvariantPublicSrcRule, Detail: "observe failed: " + err.Error()}}
	}
	var out []netcfg.Violation
	if obs.RpFilterAll != netcfg.RequiredRpFilterAll {
		out = append(out, netcfg.Violation{
			Name:   domain.InvariantRpFilterAll,
			Detail: fmt.Sprintf("net.ipv4.conf.all.rp_filter is %d, want %d", obs.RpFilterAll, netcfg.RequiredRpFilterAll),
		})
	}
	if obs.IPForward != netcfg.RequiredIPForward {
		out = append(out, netcfg.Violation{
			Name:   domain.InvariantIPForward,
			Detail: fmt.Sprintf("net.ipv4.ip_forward is %t, want %t", obs.IPForward, netcfg.RequiredIPForward),
		})
	}
	for _, p := range obs.DuplicateAddrs {
		out = append(out, netcfg.Violation{
			Name:   domain.InvariantNoDuplicateAddrs,
			Detail: "address present on more than one link: " + p.Addr().String(),
		})
	}
	m.mu.Lock()
	hosts := append([]netip.Addr(nil), m.publicHosts...)
	slots := make([]domain.Slot, 0, len(m.applied))
	for s := range m.applied {
		slots = append(slots, s)
	}
	m.mu.Unlock()
	sort.Slice(slots, func(i, j int) bool { return slots[i] < slots[j] })

	present := map[netip.Addr]bool{}
	for _, r := range obs.PublicSrcRules {
		if r.Src.IsValid() {
			present[r.Src.Addr()] = true
		}
	}
	for _, h := range hosts {
		if !present[h] {
			out = append(out, netcfg.Violation{
				Name:   domain.InvariantPublicSrcRule,
				Detail: fmt.Sprintf("no priority %d rule for public host %s", domain.RulePrioPublic, h),
			})
		}
	}
	for _, r := range obs.ForeignRuleBelowCeil {
		out = append(out, netcfg.Violation{
			Name:   domain.InvariantNoForeignRule,
			Detail: fmt.Sprintf("foreign rule at priority %d: %s", r.Priority, r.Raw),
		})
	}
	out = append(out, m.routingProbes(ctx, obs, hosts, slots)...)
	return out
}

func (m *Manager) routingProbes(ctx context.Context, obs netcfg.Observation, hosts []netip.Addr, slots []domain.Slot) []netcfg.Violation {
	var out []netcfg.Violation
	for _, h := range hosts {
		dev := linkForAddr(obs.Links, h)
		res, err := m.routeGet(ctx, m.probeDst, h, firstUID(slots))
		if err != nil {
			out = append(out, netcfg.Violation{Name: domain.InvariantCustomerLeg, Detail: err.Error()})
			continue
		}
		if netcfg.IsDongleIface(res.Dev) || (dev != "" && res.Dev != dev) {
			out = append(out, netcfg.Violation{
				Name:   domain.InvariantCustomerLeg,
				Detail: fmt.Sprintf("reply from %s leaves via %s, want %s", h, res.Dev, dev),
			})
		}
	}
	for _, s := range slots {
		res, err := m.routeGet(ctx, m.probeDst, s.HostIP(), s.UID())
		if err != nil {
			out = append(out, netcfg.Violation{Name: domain.InvariantEgressFenced, Detail: err.Error()})
		} else if res.Dev != s.IfaceName() || (res.Table != 0 && res.Table != s.RouteTable()) {
			out = append(out, netcfg.Violation{
				Name:   domain.InvariantEgressFenced,
				Detail: fmt.Sprintf("egress for slot %s leaves via %s table %d, want %s table %d", s, res.Dev, res.Table, s.IfaceName(), s.RouteTable()),
			})
		}
		res, err = m.routeGet(ctx, m.probeDst, netip.Addr{}, s.UID())
		if err != nil {
			out = append(out, netcfg.Violation{Name: domain.InvariantDNSContained, Detail: err.Error()})
		} else if res.Dev != s.IfaceName() || res.Src != s.HostIP() {
			out = append(out, netcfg.Violation{
				Name:   domain.InvariantDNSContained,
				Detail: fmt.Sprintf("unbound socket for slot %s leaves via %s src %s, want %s src %s", s, res.Dev, res.Src, s.IfaceName(), s.HostIP()),
			})
		}
	}
	return out
}

func firstUID(slots []domain.Slot) int {
	if len(slots) == 0 {
		return domain.UIDBase + 1
	}
	return slots[0].UID()
}

func linkForAddr(links map[string]netcfg.LinkState, a netip.Addr) string {
	for name, l := range links {
		for _, p := range l.Addrs {
			if p.Addr() == a {
				return name
			}
		}
	}
	return ""
}

type routeResult struct {
	Dev   string
	Src   netip.Addr
	Table int
	Raw   string
}

func (m *Manager) routeGet(ctx context.Context, dst, from netip.Addr, uid int) (routeResult, error) {
	args := []string{"route", "get", dst.String()}
	if from.IsValid() {
		args = append(args, "from", from.String())
	}
	args = append(args, "uid", strconv.Itoa(uid))
	out, err := m.exec(ctx, "ip", args...)
	if err != nil {
		return routeResult{}, err
	}
	return parseRouteGet(string(out)), nil
}

func parseRouteGet(s string) routeResult {
	res := routeResult{Raw: s}
	fields := splitFields(s)
	for i := 0; i < len(fields)-1; i++ {
		switch fields[i] {
		case "dev":
			if res.Dev == "" {
				res.Dev = fields[i+1]
			}
		case "src":
			if !res.Src.IsValid() {
				if a, err := netip.ParseAddr(fields[i+1]); err == nil {
					res.Src = a
				}
			}
		case "table":
			if res.Table == 0 {
				if v, err := strconv.Atoi(fields[i+1]); err == nil {
					res.Table = v
				}
			}
		}
	}
	return res
}

func splitFields(s string) []string {
	var out []string
	cur := make([]byte, 0, 32)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			if len(cur) > 0 {
				out = append(out, string(cur))
				cur = cur[:0]
			}
			continue
		}
		cur = append(cur, c)
	}
	if len(cur) > 0 {
		out = append(out, string(cur))
	}
	return out
}
