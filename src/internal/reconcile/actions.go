package reconcile

import (
	"sort"
	"strconv"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

const (
	ParamScope      = "scope"
	ParamIface      = "iface"
	ParamGateway    = "gateway"
	ParamSeconds    = "seconds"
	ParamObserved   = "observed"
	ParamTrigger    = "trigger"
	ParamEvict      = "evict"
	ParamExpiresAt  = "expires_at"
	ParamDongleID   = "dongle_id"
	ParamProxyID    = "proxy_id"
	ParamConnStatus = "conn_status"
	ParamUnit       = "unit"
)

const (
	ScopeGlobal = "global"
	ScopeSlot   = "slot"
)

const (
	ReasonGlobalRoutingMissing = "global public source rule or route table names are missing"
	ReasonLinkMissing          = "interface is not present"
	ReasonAddressMissing       = "interface is missing its static host address"
	ReasonRuleMissing          = "slot routing rule is missing"
	ReasonRouteMissing         = "slot route table has no default route"
	ReasonFirewallSetMissing   = "interface is not a member of the firewall dongle set"
	ReasonMaxIdleNotDisabled   = "dongle would drop an idle data session"
	ReasonProxyExpired         = "proxy expiry passed"
	ReasonProxyDisabled        = "proxy is disabled"
	ReasonProxySuspended       = "proxy is suspended"
	ReasonSlotHasNoDongle      = "slot holds no dongle so the proxy has no egress"
	ReasonProxyNotRunning      = "proxy unit is not running"
	ReasonProxyNotBound        = "proxy unit is running but a listener is not bound"
	ReasonProxyProbeFailed     = "proxy unit is bound but the authenticated probe failed"
	ReasonDongleUnreachable    = "dongle has not answered the hilink api"
	ReasonDataSessionDown      = "dongle data session is not connected"
)

type Actions []Action

func (as Actions) Kinds() []ActionKind {
	out := make([]ActionKind, 0, len(as))
	for _, a := range as {
		out = append(out, a.Kind())
	}
	return out
}

func (as Actions) Targets() []string {
	out := make([]string, 0, len(as))
	for _, a := range as {
		out = append(out, a.Target())
	}
	return out
}

func (as Actions) OfKind(kinds ...ActionKind) Actions {
	want := make(map[ActionKind]bool, len(kinds))
	for _, k := range kinds {
		want[k] = true
	}
	out := make(Actions, 0, len(as))
	for _, a := range as {
		if want[a.Kind()] {
			out = append(out, a)
		}
	}
	return out
}

func (as Actions) Destructive() Actions {
	out := make(Actions, 0, len(as))
	for _, a := range as {
		if a.Kind().Destructive() {
			out = append(out, a)
		}
	}
	return out
}

func (as Actions) CountByKind() map[ActionKind]int {
	out := make(map[ActionKind]int, len(as))
	for _, a := range as {
		out[a.Kind()]++
	}
	return out
}

func Destructive(actions []Action) []Action {
	return Actions(actions).Destructive()
}

func dropDestructive(actions []Action) []Action {
	out := make([]Action, 0, len(actions))
	for _, a := range actions {
		if a.Kind().Destructive() {
			continue
		}
		out = append(out, a)
	}
	return out
}

func act(kind ActionKind, subject domain.SubjectType, id string, slot domain.Slot, reason string, params map[string]string) BaseAction {
	return BaseAction{
		Op:        kind,
		Subject:   subject,
		SubjectID: id,
		Slot:      slot,
		Reason:    reason,
		Params:    params,
	}
}

func paramsOf(a Action) map[string]string {
	b, ok := a.(BaseAction)
	if !ok {
		return nil
	}
	return b.Params
}

func param(a Action, key string) string {
	return paramsOf(a)[key]
}

func slotOf(a Action) domain.Slot {
	b, ok := a.(BaseAction)
	if !ok {
		return 0
	}
	return b.Slot
}

func reasonOf(a Action) string {
	b, ok := a.(BaseAction)
	if !ok {
		return ""
	}
	return b.Reason
}

func subjectOf(a Action) (domain.SubjectType, string) {
	b, ok := a.(BaseAction)
	if !ok {
		return "", ""
	}
	return b.Subject, b.SubjectID
}

func sortedSlots(m map[domain.Slot]domain.SlotRow) []domain.Slot {
	out := make([]domain.Slot, 0, len(m))
	for s := range m {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func itoa(n int) string { return strconv.Itoa(n) }

func i64toa(n int64) string { return strconv.FormatInt(n, 10) }
