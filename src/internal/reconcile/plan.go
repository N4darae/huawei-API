package reconcile

import (
	"net/netip"

	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/netcfg"
	"github.com/n4darae/huawei-API/src/internal/proxysup"
)

const UnreachableRebootAfter = 60_000

func Plan(w World) []Action {
	out := make([]Action, 0, 2*len(w.Desired.Slots)+1)
	out = append(out, planNode(w)...)

	proxies := w.Desired.ProxiesBySlot()
	for _, s := range sortedSlots(w.Desired.Slots) {
		out = append(out, planSlot(w, w.Desired.Slots[s], proxies)...)
	}

	if w.InStartupGrace() {
		return dropDestructive(out)
	}
	return out
}

func planNode(w World) []Action {
	if len(w.Observed.Net.PublicSrcRules) > 0 && w.Observed.Net.RouteTableNamesOK {
		return nil
	}
	if _, busy := w.ActiveOpOn(domain.SubjectNode, w.Desired.Node.ID); busy {
		return nil
	}
	return []Action{act(ActApplyNetcfg, domain.SubjectNode, w.Desired.Node.ID, 0,
		ReasonGlobalRoutingMissing, map[string]string{ParamScope: ScopeGlobal})}
}

func planSlot(w World, row domain.SlotRow, proxies map[string]domain.Proxy) []Action {
	s := row.Slot
	if !s.Valid() {
		return nil
	}
	if _, busy := w.ActiveOpOn(domain.SubjectSlot, row.ID); busy {
		return nil
	}

	px, hasProxy := proxies[row.ID]
	if hasProxy {
		if _, busy := w.ActiveOpOn(domain.SubjectProxy, px.ID); busy {
			return nil
		}
	}

	dongle, hasDongle := w.Desired.DongleFor(row)
	if hasDongle {
		if _, busy := w.ActiveOpOn(domain.SubjectDongle, dongle.ID); busy {
			return nil
		}
	}

	out := make([]Action, 0, 4)

	ready, reason := egressReady(w, row)
	if !ready {
		out = append(out, act(ActApplyNetcfg, domain.SubjectSlot, row.ID, s, reason,
			map[string]string{ParamScope: ScopeSlot, ParamIface: row.IfName}))
	}

	if _, present := w.Observed.Net.Links[row.IfName]; present && w.Observed.NftTablePresent {
		if _, known := w.Observed.Fenced[row.IfName]; !known {
			out = append(out, act(ActAddFwDongle, domain.SubjectSlot, row.ID, s, ReasonFirewallSetMissing,
				map[string]string{ParamIface: row.IfName, ParamGateway: s.GatewayIP().String()}))
		}
	}

	dev, seen := w.Observed.Devices[s]

	if hasDongle && seen && dev.Reachable && dev.MaxIdleTime != device.MaxIdleTimeDisabled {
		out = append(out, act(ActSetMaxIdle, domain.SubjectDongle, dongle.ID, s, ReasonMaxIdleNotDisabled,
			map[string]string{
				ParamSeconds:  itoa(device.MaxIdleTimeDisabled),
				ParamObserved: itoa(dev.MaxIdleTime),
			}))
	}

	if hasProxy {
		out = append(out, planProxy(w, row, px, ready)...)
	}
	if hasDongle {
		out = append(out, planRecovery(w, row, dongle, px, hasProxy, dev, seen)...)
	}
	return out
}

func planProxy(w World, row domain.SlotRow, px domain.Proxy, egressReady bool) []Action {
	s := row.Slot
	st := w.Observed.ProxyStatus[s]
	nowMS := domain.UnixMillis(w.Now)

	switch px.DesiredState(nowMS) {
	case domain.ProxyStateExpired:
		return []Action{
			act(ActMarkExpired, domain.SubjectProxy, px.ID, s, ReasonProxyExpired,
				map[string]string{ParamExpiresAt: expiresAt(px)}),
			act(ActEvictProxy, domain.SubjectProxy, px.ID, s, ReasonProxyExpired,
				map[string]string{ParamEvict: "true", ParamExpiresAt: expiresAt(px)}),
		}
	case domain.ProxyStateDisabled:
		if !st.Running {
			return nil
		}
		return []Action{act(ActStopProxy, domain.SubjectProxy, px.ID, s, ReasonProxyDisabled, nil)}
	case domain.ProxyStateSuspended:
		if !st.Running {
			return nil
		}
		return []Action{act(ActEvictProxy, domain.SubjectProxy, px.ID, s, ReasonProxySuspended,
			map[string]string{ParamEvict: "true"})}
	}

	if !row.Occupied() {
		if !st.Running {
			return nil
		}
		return []Action{act(ActStopProxy, domain.SubjectProxy, px.ID, s, ReasonSlotHasNoDongle, nil)}
	}
	if !egressReady {
		return nil
	}
	if reason, drifted := proxyDrift(st); drifted {
		return []Action{act(ActApplyProxy, domain.SubjectProxy, px.ID, s, reason,
			map[string]string{ParamUnit: s.ProxyUnit()})}
	}
	return nil
}

func planRecovery(w World, row domain.SlotRow, d domain.Dongle, px domain.Proxy, hasProxy bool, dev DeviceObservation, seen bool) []Action {
	if !seen || !d.AutoRecoverEnabled {
		return nil
	}
	if dev.Sim.Locked() || dev.LoginNeeded {
		return nil
	}

	if !dev.Reachable {
		since := dev.ObservedAt
		if since.IsZero() {
			since = dev.FailingSince
		}
		if since.IsZero() || domain.UnixMillis(w.Now)-domain.UnixMillis(since) < UnreachableRebootAfter {
			return nil
		}
		if !w.Budgets.RebootAllowed(d.ID, w.Now) {
			return nil
		}
		return []Action{act(ActRebootDongle, domain.SubjectDongle, d.ID, row.Slot, ReasonDongleUnreachable,
			map[string]string{ParamTrigger: string(domain.TriggerAutoRecovery)})}
	}

	if dev.Conn.Connected() || !hasProxy {
		return nil
	}
	if px.DesiredState(domain.UnixMillis(w.Now)) != domain.ProxyStateActive {
		return nil
	}
	if !w.Budgets.RotateAllowed(px.ID, w.Now) {
		return nil
	}
	return []Action{act(ActRecoverRotate, domain.SubjectProxy, px.ID, row.Slot, ReasonDataSessionDown,
		map[string]string{
			ParamTrigger:    string(domain.TriggerAutoRecovery),
			ParamDongleID:   d.ID,
			ParamConnStatus: itoa(int(dev.Conn)),
		})}
}

func egressReady(w World, row domain.SlotRow) (bool, string) {
	link, ok := w.Observed.Net.Links[row.IfName]
	if !ok {
		return false, ReasonLinkMissing
	}
	if !hasPrefix(link.Addrs, row.Slot.HostPrefix()) {
		return false, ReasonAddressMissing
	}
	if !hasRule(w.Observed.Net.Rules, row.Slot.RulePrioSrc()) ||
		!hasRule(w.Observed.Net.Rules, row.Slot.RulePrioUID()) {
		return false, ReasonRuleMissing
	}
	if !hasDefaultRoute(w.Observed.Net.Routes[row.Slot.RouteTable()], row.IfName) {
		return false, ReasonRouteMissing
	}
	return true, ""
}

func proxyDrift(st proxysup.Status) (string, bool) {
	switch {
	case !st.Running:
		return ReasonProxyNotRunning, true
	case !st.SocksBound || !st.HTTPBound:
		return ReasonProxyNotBound, true
	case !st.ProbeOK:
		return ReasonProxyProbeFailed, true
	}
	return "", false
}

func hasPrefix(have []netip.Prefix, want netip.Prefix) bool {
	for _, p := range have {
		if p == want {
			return true
		}
	}
	return false
}

func hasRule(rules []netcfg.RuleState, priority int) bool {
	for _, r := range rules {
		if r.Priority == priority {
			return true
		}
	}
	return false
}

func hasDefaultRoute(routes []netcfg.RouteState, dev string) bool {
	for _, r := range routes {
		if r.Dev != dev {
			continue
		}
		if r.Dst.Bits() == 0 || !r.Dst.IsValid() {
			return true
		}
	}
	return false
}

func expiresAt(px domain.Proxy) string {
	if px.ExpiresAt == nil {
		return ""
	}
	return i64toa(*px.ExpiresAt)
}
