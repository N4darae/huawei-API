package devops

import (
	"context"
	"reflect"
	"testing"

	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/domain"
)

func TestSetLanIPSurvivesTheResponseThatNeverArrives(t *testing.T) {
	var farmRef *harness
	watch := func(s domain.Slot) bool {
		return farmRef.farm.BaseURLForAddr(s.GatewayIP()) != ""
	}
	h := newHarness(t, harnessOptions{FactoryLAN: true, Watch: watch})
	farmRef = h

	gw := domain.Slot(1).GatewayIP()
	op := h.await(h.svc.SetLanIP(context.Background(), dongleID(1), gw))
	if op.State != domain.OpSucceeded {
		t.Fatalf("SetLanIP finished %q (%q), want succeeded", op.State, op.Error)
	}
	if got, want := h.steps(dongleID(1)), LanIPSteps(); !reflect.DeepEqual(got, want) {
		t.Fatalf("lan ip steps are %v, want %v", got, want)
	}
	res := decodeResult[LanIPResult](t, op)
	if !res.PostTimedOut {
		t.Fatalf("lan ip result is %+v, want the dhcp post recorded as never answered", res)
	}
	if !res.Rediscovered || !res.Moved || !res.Supported {
		t.Fatalf("lan ip result is %+v, want the dongle rediscovered at its new address", res)
	}
	if res.From != device.FactoryDefaultAddr.String() || res.To != gw.String() {
		t.Fatalf("lan ip result is %+v, want a move from the factory default to %s", res, gw)
	}

	if applied := h.net.Applied(); len(applied) != 1 || applied[0] != 1 {
		t.Fatalf("netcfg was applied for %v, want exactly slot 1", applied)
	}
	if seen := h.net.Observations(); len(seen) != 1 || seen[0] {
		t.Fatalf("the .network file was written after the dongle had already moved: %v", seen)
	}

	d, err := h.db.Dongles().Get(context.Background(), dongleID(1))
	if err != nil {
		t.Fatalf("Get dongle: %v", err)
	}
	if !d.LanIPChangeSupported {
		t.Fatal("a successful lan ip change did not record the capability")
	}
	if h.farm.Device(1).LANAddr() != gw {
		t.Fatalf("the simulated dongle sits at %s, want %s", h.farm.Device(1).LANAddr(), gw)
	}
}

func TestSetLanIPIsIdempotentWhenTheDongleAlreadyMoved(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	gw := domain.Slot(1).GatewayIP()
	op := h.await(h.svc.SetLanIP(context.Background(), dongleID(1), gw))
	if op.State != domain.OpSucceeded {
		t.Fatalf("SetLanIP finished %q (%q)", op.State, op.Error)
	}
	res := decodeResult[LanIPResult](t, op)
	if res.Moved || !res.Supported {
		t.Fatalf("lan ip result is %+v, want a no-op that still records support", res)
	}
	if applied := h.net.Applied(); len(applied) != 0 {
		t.Fatalf("a no-op lan ip change rewrote the network files: %v", applied)
	}
}

func TestSetLanIPRejectsAGatewayOutsideTheFrozenPlan(t *testing.T) {
	h := newHarness(t, harnessOptions{FactoryLAN: true})
	_, err := h.svc.SetLanIP(context.Background(), dongleID(1), domain.Slot(7).GatewayIP())
	requireErrorIs(t, err, ErrLanIPNotPlanned, "SetLanIP to another slot address")
}

func TestSetLanIPWithoutANetcfgManagerIsRefused(t *testing.T) {
	h := newHarness(t, harnessOptions{FactoryLAN: true, NoNetcfg: true})
	_, err := h.svc.SetLanIP(context.Background(), dongleID(1), domain.Slot(1).GatewayIP())
	requireErrorIs(t, err, ErrNoNetcfg, "SetLanIP without a netcfg manager")
}

func TestSetLanIPMarksTheSlotUnsupportedWhenTheDongleRefusesTheApi(t *testing.T) {
	h := newHarness(t, harnessOptions{FactoryLAN: true})
	h.hooks.set(func(x *hooks) { x.setDHCPErr = domain.HiLinkError(domain.CodeSystemNoSupport, "", "dhcp/settings") })

	op := h.await(h.svc.SetLanIP(context.Background(), dongleID(1), domain.Slot(1).GatewayIP()))
	if op.State != domain.OpFailed || op.Error != ReasonLanIPUnsupported {
		t.Fatalf("SetLanIP finished %q (%q), want failed with %q", op.State, op.Error, ReasonLanIPUnsupported)
	}
	d, err := h.db.Dongles().Get(context.Background(), dongleID(1))
	if err != nil {
		t.Fatalf("Get dongle: %v", err)
	}
	if d.LanIPChangeSupported {
		t.Fatal("a dongle that refused dhcp/settings is still marked as supporting the change")
	}
}
