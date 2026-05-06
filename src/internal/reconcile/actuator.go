package reconcile

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/fw"
	"github.com/n4darae/huawei-API/src/internal/netcfg"
	"github.com/n4darae/huawei-API/src/internal/proxysup"
	"github.com/n4darae/huawei-API/src/internal/store"
)

var (
	ErrNoOps      = errors.New("reconcile: recovery is not wired; no Ops implementation was supplied")
	ErrFenced     = errors.New("reconcile: an operation is already live on the subject")
	ErrUnknownAct = errors.New("reconcile: unknown action kind")
	ErrNoRepos    = errors.New("reconcile: a store.Repos is required")
)

type Ops interface {
	RecoverRotate(ctx context.Context, proxyID, reason string) (domain.Operation, error)
	RebootDongle(ctx context.Context, dongleID, reason string) (domain.Operation, error)
}

type ActuatorDeps struct {
	Net             netcfg.Manager
	FW              fw.Firewall
	Proxy           proxysup.Supervisor
	Dev             device.Registry
	Repos           store.Repos
	Ops             Ops
	Clock           Clock
	NServerFallback netip.Addr
}

type Actuator struct {
	deps ActuatorDeps
}

type Outcome struct {
	Kind        ActionKind
	Target      string
	Slot        domain.Slot
	Reason      string
	Skipped     bool
	SkipReason  string
	OperationID string
	Err         error
}

func NewActuator(d ActuatorDeps) (*Actuator, error) {
	if d.Repos == nil {
		return nil, ErrNoRepos
	}
	if d.Clock == nil {
		d.Clock = domain.SystemClock()
	}
	return &Actuator{deps: d}, nil
}

func (a *Actuator) Apply(ctx context.Context, w World, action Action) Outcome {
	subject, id := subjectOf(action)
	out := Outcome{
		Kind:   action.Kind(),
		Target: action.Target(),
		Slot:   slotOf(action),
		Reason: reasonOf(action),
	}

	if action.Kind().Destructive() {
		if live, busy, err := a.liveOperation(ctx, subject, id); err != nil {
			out.Err = err
			return out
		} else if busy {
			out.Skipped = true
			out.SkipReason = ErrFenced.Error()
			out.OperationID = live.ID
			return out
		}
	}

	switch action.Kind() {
	case ActApplyNetcfg:
		out.Err = a.applyNetcfg(ctx, w, action)
	case ActAddFwDongle:
		out.Err = a.addFwDongle(ctx, action)
	case ActSetMaxIdle:
		out.Err = a.setMaxIdle(ctx, action)
	case ActApplyProxy:
		out.Err = a.applyProxy(ctx, w, action, id)
	case ActStopProxy:
		out.Err = a.stopProxy(ctx, action, false)
	case ActEvictProxy:
		out.Err = a.stopProxy(ctx, action, true)
	case ActMarkExpired:
		out.Err = a.markExpired(ctx, id)
	case ActRecoverRotate:
		out.OperationID, out.Err = a.recoverRotate(ctx, id, reasonOf(action))
	case ActRebootDongle:
		out.OperationID, out.Err = a.rebootDongle(ctx, id, reasonOf(action))
	default:
		out.Err = fmt.Errorf("%w: %q", ErrUnknownAct, string(action.Kind()))
	}
	return out
}

func (a *Actuator) liveOperation(ctx context.Context, subject domain.SubjectType, id string) (domain.Operation, bool, error) {
	op, err := a.deps.Repos.Operations().FindActive(ctx, subject, id)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Operation{}, false, nil
	}
	if err != nil {
		return domain.Operation{}, false, err
	}
	return op, true, nil
}

func (a *Actuator) applyNetcfg(ctx context.Context, w World, action Action) error {
	if a.deps.Net == nil {
		return nil
	}
	if param(action, ParamScope) == ScopeGlobal {
		if err := a.deps.Net.EnsureRouteTableNames(ctx); err != nil {
			return err
		}
		hosts := []netip.Addr{}
		if w.Desired.Node.PublicHost.IsValid() {
			hosts = append(hosts, w.Desired.Node.PublicHost)
		}
		return a.deps.Net.EnsureGlobal(ctx, hosts)
	}

	slot := slotOf(action)
	row, ok := w.Desired.Slots[slot]
	if !ok {
		return fmt.Errorf("reconcile: slot %d left the desired state: %w", int(slot), domain.ErrNotFound)
	}
	mac := ""
	if link, present := w.Observed.Net.Links[row.IfName]; present {
		mac = link.MAC
	}
	return a.deps.Net.ApplySlot(ctx, slot, row.IDPath, mac)
}

func (a *Actuator) addFwDongle(ctx context.Context, action Action) error {
	if a.deps.FW == nil {
		return nil
	}
	gw, err := netip.ParseAddr(param(action, ParamGateway))
	if err != nil {
		gw = slotOf(action).GatewayIP()
	}
	return a.deps.FW.AddDongle(ctx, param(action, ParamIface), gw)
}

func (a *Actuator) setMaxIdle(ctx context.Context, action Action) error {
	if a.deps.Dev == nil {
		return nil
	}
	dev, err := a.deps.Dev.ForSlot(ctx, slotOf(action))
	if err != nil {
		return err
	}
	return dev.SetMaxIdleTime(ctx, device.MaxIdleTimeDisabled)
}

func (a *Actuator) applyProxy(ctx context.Context, w World, action Action, proxyID string) error {
	if a.deps.Proxy == nil {
		return nil
	}
	px, ok := w.Desired.Proxies[proxyID]
	if !ok {
		return fmt.Errorf("reconcile: proxy %q left the desired state: %w", proxyID, domain.ErrNotFound)
	}
	row, ok := w.Desired.Slots[slotOf(action)]
	if !ok {
		return fmt.Errorf("reconcile: slot %d left the desired state: %w", int(slotOf(action)), domain.ErrNotFound)
	}
	_, err := a.deps.Proxy.Apply(ctx, SpecFor(w.Desired.Node, row, px, a.deps.NServerFallback))
	return err
}

func (a *Actuator) stopProxy(ctx context.Context, action Action, evict bool) error {
	if a.deps.Proxy == nil {
		return nil
	}
	return a.deps.Proxy.Stop(ctx, slotOf(action), evict)
}

func (a *Actuator) markExpired(ctx context.Context, proxyID string) error {
	err := a.deps.Repos.Proxies().SetEnabled(ctx, proxyID, false)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	return err
}

func (a *Actuator) recoverRotate(ctx context.Context, proxyID, reason string) (string, error) {
	if a.deps.Ops == nil {
		return "", ErrNoOps
	}
	op, err := a.deps.Ops.RecoverRotate(ctx, proxyID, reason)
	return op.ID, err
}

func (a *Actuator) rebootDongle(ctx context.Context, dongleID, reason string) (string, error) {
	if a.deps.Ops == nil {
		return "", ErrNoOps
	}
	op, err := a.deps.Ops.RebootDongle(ctx, dongleID, reason)
	return op.ID, err
}
