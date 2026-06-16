package devops

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/domain"
)

func TestRebootWalksItsPublicStepsAndBringsTheDataSessionBack(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	op := h.await(h.svc.Reboot(context.Background(), dongleID(1)))
	if op.State != domain.OpSucceeded {
		t.Fatalf("reboot finished %q (%q), want succeeded", op.State, op.Error)
	}
	if got, want := h.steps(dongleID(1)), RebootSteps(); !reflect.DeepEqual(got, want) {
		t.Fatalf("reboot steps are %v, want %v", got, want)
	}
	res := decodeResult[RebootResult](t, op)
	if !res.Reachable || !res.DownObserved {
		t.Fatalf("reboot result is %+v, want the dongle observed down and back up", res)
	}
	if res.ConnStatus != int(device.ConnConnected) {
		t.Fatalf("reboot left connection status %d, want 901", res.ConnStatus)
	}
	if h.farm.Device(1).ConnectionStatus() != device.ConnConnected {
		t.Fatal("the dongle was left without a data session after a reboot")
	}
}

func TestAdminRebootIsRefusedWhileARotateIsLiveOnTheProxy(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	live := h.startLiveRotate(1)

	_, err := h.svc.Reboot(context.Background(), dongleID(1))
	requireErrorIs(t, err, domain.ErrOpInProgress, "Reboot during a live rotate")

	var accessor interface{ ActiveOperationID() string }
	if !errors.As(err, &accessor) || accessor.ActiveOperationID() != live.ID {
		t.Fatalf("refusal %v does not carry the live operation id %q", err, live.ID)
	}
}

func TestAutoRecoveryRebootHonoursTheDailyBudget(t *testing.T) {
	to := DefaultTimeouts()
	to.PollInterval = time.Second
	to.RebootBudgetPerDay = 1
	to.RebootCooldown = 0
	h := newHarness(t, harnessOptions{Timeouts: &to})

	if _, err := h.svc.RebootAuto(context.Background(), dongleID(1), "unreachable"); err != nil {
		t.Fatalf("first RebootAuto: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rows, err := h.db.Operations().ListActive(ctx)
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	for _, r := range rows {
		if _, err := h.svc.Wait(ctx, r.ID); err != nil {
			t.Fatalf("Wait: %v", err)
		}
	}

	_, err = h.svc.RebootAuto(context.Background(), dongleID(1), "unreachable")
	requireErrorIs(t, err, ErrRebootBudget, "second RebootAuto in the same day")
}

func TestAutoRecoveryRebootHonoursTheCooldown(t *testing.T) {
	to := DefaultTimeouts()
	to.PollInterval = time.Second
	to.RebootBudgetPerDay = 4
	to.RebootCooldown = 30 * time.Minute
	h := newHarness(t, harnessOptions{Timeouts: &to})

	op := h.await(rebootAutoOp(t, h, "unreachable"))
	if op.State != domain.OpSucceeded {
		t.Fatalf("first reboot finished %q (%q)", op.State, op.Error)
	}
	_, err := h.svc.RebootAuto(context.Background(), dongleID(1), "unreachable")
	requireErrorIs(t, err, ErrRebootBudget, "RebootAuto inside the cooldown")
}

func rebootAutoOp(t *testing.T, h *harness, reason string) (*domain.Operation, error) {
	t.Helper()
	op, err := h.svc.RebootAuto(context.Background(), dongleID(1), reason)
	if err != nil {
		return nil, err
	}
	return &op, nil
}

func TestConcurrentDeviceOperationsOnOneDongleConflict(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	live := h.startLiveRotateOnDongle(1)

	_, err := h.svc.Reboot(context.Background(), dongleID(1))
	requireErrorIs(t, err, domain.ErrOpInProgress, "Reboot with another dongle operation live")
	var conflict *ConflictError
	if !errors.As(err, &conflict) || conflict.OperationID != live.ID {
		t.Fatalf("refusal %v does not carry %q", err, live.ID)
	}
}
