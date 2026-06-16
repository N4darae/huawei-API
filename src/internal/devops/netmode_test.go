package devops

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/domain"
)

func TestSetNetModeWhileTheDialupSessionIsUpFailsWith112001(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	dev, err := h.svc.deps.Dev.ForSlot(context.Background(), 1)
	if err != nil {
		t.Fatalf("ForSlot: %v", err)
	}
	if !h.farm.Device(1).DataOn() {
		t.Fatal("the simulated dongle is not dialled up, the test would prove nothing")
	}
	err = dev.SetNetMode(context.Background(), device.NetModeLTE)
	requireErrorIs(t, err, domain.ErrBusy, "SetNetMode with the dialup session up")

	code, ok := domain.HiLinkCodeOf(err)
	if !ok || code != domain.CodeSetNetModeWhenDialup {
		t.Fatalf("SetNetMode returned code %d (ok=%v), want 112001", code, ok)
	}
}

func TestSetNetModeSucceedsWithTheDataOffFirstSequence(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	op := h.await(h.svc.SetNetMode(context.Background(), dongleID(1), device.NetModeLTE))
	if op.State != domain.OpSucceeded {
		t.Fatalf("SetNetMode finished %q (%q), want succeeded", op.State, op.Error)
	}
	if got, want := h.steps(dongleID(1)), NetModeSteps(); !reflect.DeepEqual(got, want) {
		t.Fatalf("net mode steps are %v, want %v", got, want)
	}
	res := decodeResult[NetModeResult](t, op)
	if !res.Changed || res.To != string(device.NetModeLTE) {
		t.Fatalf("net mode result is %+v, want a change to lte", res)
	}
	got, err := h.svc.NetMode(context.Background(), dongleID(1))
	if err != nil || got != device.NetModeLTE {
		t.Fatalf("dongle reports net mode %q (err %v), want lte", got, err)
	}
	if h.farm.Device(1).ConnectionStatus() != device.ConnConnected {
		t.Fatal("the data session was not restored after the net mode change")
	}
}

func TestSetNetModeIsANoOpWhenTheModeAlreadyMatches(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	op := h.await(h.svc.SetNetMode(context.Background(), dongleID(1), device.NetModeAuto))
	if op.State != domain.OpSucceeded {
		t.Fatalf("SetNetMode finished %q (%q)", op.State, op.Error)
	}
	if res := decodeResult[NetModeResult](t, op); res.Changed {
		t.Fatalf("net mode result is %+v, want no change", res)
	}
	if !h.farm.Device(1).DataOn() {
		t.Fatal("a no-op net mode change still dropped the data session")
	}
}

func TestSetNetModeRestoresTheDataSessionWhenTheModePostIsRejected(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	h.hooks.set(func(x *hooks) {
		x.setNetModeErr = domain.HiLinkError(domain.CodeSetNetModeWhenDialup, "", "net/net-mode")
	})

	op := h.await(h.svc.SetNetMode(context.Background(), dongleID(1), device.NetModeLTE))
	if op.State != domain.OpFailed || op.Error != ReasonSetModeRejected {
		t.Fatalf("SetNetMode finished %q (%q), want failed with %q", op.State, op.Error, ReasonSetModeRejected)
	}
	if !h.farm.Device(1).DataOn() {
		t.Fatal("a rejected net mode change left the customer without a data session")
	}
}

func TestSetNetModeIsRefusedWhileARotateIsLive(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	live := h.startLiveRotate(1)

	_, err := h.svc.SetNetMode(context.Background(), dongleID(1), device.NetModeLTE)
	requireErrorIs(t, err, domain.ErrOpInProgress, "SetNetMode during a live rotate")

	var conflict *ConflictError
	if !errors.As(err, &conflict) || conflict.OperationID != live.ID {
		t.Fatalf("refusal %v does not fence against the live rotate %q", err, live.ID)
	}
}

func TestSetNetModeRejectsAnUnknownMode(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	_, err := h.svc.SetNetMode(context.Background(), dongleID(1), device.NetMode("5g"))
	requireErrorIs(t, err, domain.ErrInvalid, "SetNetMode with an unknown mode")
}
