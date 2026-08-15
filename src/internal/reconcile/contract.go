package reconcile

import (
	"net/netip"
	"time"

	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/netcfg"
	"github.com/n4darae/huawei-API/src/internal/proxysup"
)

type Clock = domain.Clock

type DesiredState struct {
	Node    domain.Node
	Slots   map[domain.Slot]domain.SlotRow
	Dongles map[string]domain.Dongle
	Proxies map[string]domain.Proxy
	AuthIPs map[string][]netip.Prefix
}

type DeviceObservation struct {
	Reachable    bool
	Conn         device.ConnStatus
	Sim          device.SimState
	NetMode      device.NetMode
	WanIP        netip.Addr
	Signal       device.Signal
	Traffic      device.Traffic
	MaxIdleTime  int
	LoginNeeded  bool
	ObservedAt   time.Time
	FailingSince time.Time
	Err          string
}

type ObservedState struct {
	Net             netcfg.Observation
	NftTablePresent bool
	Fenced          map[string]bool
	ProxyStatus     map[domain.Slot]proxysup.Status
	Devices         map[domain.Slot]DeviceObservation
	LastSweepAt     time.Time
	SweepsCompleted int
}

type Budgets struct {
	RebootPerDay        int
	RebootCooldown      time.Duration
	RebootUsed          map[string]int
	LastRebootAt        map[string]time.Time
	RotateInFlight      int
	MaxConcurrentRotate int
	MinRotateInterval   time.Duration
	LastRotateAt        map[string]time.Time
}

func (b Budgets) RebootAllowed(dongleID string, now time.Time) bool {
	if b.RebootUsed[dongleID] >= b.RebootPerDay {
		return false
	}
	last, ok := b.LastRebootAt[dongleID]
	if ok && now.Sub(last) < b.RebootCooldown {
		return false
	}
	return true
}

func (b Budgets) RotateAllowed(proxyID string, now time.Time) bool {
	if b.MaxConcurrentRotate > 0 && b.RotateInFlight >= b.MaxConcurrentRotate {
		return false
	}
	last, ok := b.LastRotateAt[proxyID]
	if ok && now.Sub(last) < b.MinRotateInterval {
		return false
	}
	return true
}

type World struct {
	Now              time.Time
	HostBootedAt     time.Time
	ProcessStartedAt time.Time
	StartupGrace     time.Duration
	CacheWarm        bool
	Desired          DesiredState
	Observed         ObservedState
	Budgets          Budgets
	ActiveOps        map[string]domain.Operation
}

func (w World) GraceUntil() time.Time {
	base := w.HostBootedAt
	if w.ProcessStartedAt.After(base) {
		base = w.ProcessStartedAt
	}
	return base.Add(w.StartupGrace)
}

func (w World) InStartupGrace() bool {
	return !w.CacheWarm || w.Now.Before(w.GraceUntil())
}

func (w World) ActiveOpOn(subject domain.SubjectType, id string) (domain.Operation, bool) {
	op, ok := w.ActiveOps[string(subject)+":"+id]
	return op, ok
}

type ActionKind string

const (
	ActApplyNetcfg   ActionKind = "apply_netcfg"
	ActApplyProxy    ActionKind = "apply_proxy"
	ActStopProxy     ActionKind = "stop_proxy"
	ActEvictProxy    ActionKind = "evict_proxy"
	ActAddFwDongle   ActionKind = "add_fw_dongle"
	ActRecoverRotate ActionKind = "recover_rotate"
	ActRebootDongle  ActionKind = "reboot_dongle"
	ActSetMaxIdle    ActionKind = "set_max_idle"
	ActMarkExpired   ActionKind = "mark_expired"
)

func AllActionKinds() []ActionKind {
	return []ActionKind{
		ActApplyNetcfg,
		ActApplyProxy,
		ActStopProxy,
		ActEvictProxy,
		ActAddFwDongle,
		ActRecoverRotate,
		ActRebootDongle,
		ActSetMaxIdle,
		ActMarkExpired,
	}
}

func (k ActionKind) Destructive() bool {
	return k == ActRecoverRotate || k == ActRebootDongle || k == ActEvictProxy
}

type Action interface {
	Kind() ActionKind
	Target() string
}

type BaseAction struct {
	Op        ActionKind
	Subject   domain.SubjectType
	SubjectID string
	Slot      domain.Slot
	Reason    string
	Params    map[string]string
}

var _ Action = BaseAction{}

func (a BaseAction) Kind() ActionKind { return a.Op }

func (a BaseAction) Target() string { return string(a.Subject) + ":" + a.SubjectID }
