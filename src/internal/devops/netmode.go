package devops

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/domain"
)

type NetModeResult struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Changed    bool   `json:"changed"`
	ConnStatus int    `json:"conn_status"`
	DurationMS int    `json:"duration_ms"`
	Note       string `json:"note,omitempty"`
}

func (s *Service) SetNetMode(ctx context.Context, dongleID string, m device.NetMode) (*domain.Operation, error) {
	return s.SetNetModeAs(ctx, dongleID, m, AdminActor())
}

func (s *Service) SetNetModeAs(ctx context.Context, dongleID string, m device.NetMode, a Actor) (*domain.Operation, error) {
	if !m.Valid() {
		return nil, fmt.Errorf("%w: net mode %q", domain.ErrInvalid, string(m))
	}
	t, err := s.target(ctx, dongleID)
	if err != nil {
		return nil, err
	}
	if err := s.checkConflict(ctx, domain.SubjectDongle, dongleID); err != nil {
		return nil, err
	}
	if err := s.checkRotateFence(ctx, t); err != nil {
		return nil, err
	}
	return s.start(ctx, domain.OpSetNetMode, domain.SubjectDongle, dongleID, a, s.to.NetModeDeadline,
		func(ctx context.Context, op *domain.Operation) (any, string, error) {
			return s.runNetMode(ctx, op, t, m)
		})
}

func (s *Service) runNetMode(ctx context.Context, op *domain.Operation, t dongleTarget, want device.NetMode) (any, string, error) {
	start := s.deps.Clock.Now()
	steps := NetModeSteps()
	res := NetModeResult{To: string(want)}

	s.step(ctx, op, StepPrecheck, steps)
	dev, err := s.deps.Dev.ForSlot(ctx, t.slot.Slot)
	if err != nil {
		return res, ReasonDeviceUnreachable, err
	}
	cur, err := dev.NetMode(ctx)
	if err != nil {
		return res, ReasonDeviceUnreachable, err
	}
	res.From = string(cur)
	if cur == want {
		res.DurationMS = s.elapsedMS(start)
		s.step(ctx, op, StepDone, steps)
		return res, "", nil
	}

	dataOff := false
	defer func() {
		if !dataOff {
			return
		}
		back, cancel := context.WithTimeout(context.Background(), s.to.ConnectTimeout)
		defer cancel()
		_ = dev.DataSwitch(back, true)
	}()

	s.step(ctx, op, StepDataOff, steps)
	if err := dev.DataSwitch(ctx, false); err != nil {
		return res, ReasonDeviceUnreachable, err
	}
	dataOff = true

	s.step(ctx, op, StepWaitDisconnect, steps)
	if err := s.pollConn(ctx, dev, device.ConnDisconnected, s.to.ConnectTimeout); err != nil {
		res.DurationMS = s.elapsedMS(start)
		return res, ReasonNoDataSession, err
	}

	s.step(ctx, op, StepSetMode, steps)
	if err := dev.SetNetMode(ctx, want); err != nil {
		res.DurationMS = s.elapsedMS(start)
		if errors.Is(err, domain.ErrBusy) {
			return res, ReasonSetModeRejected, fmt.Errorf("%w: the dialup session was still up when net-mode was posted", err)
		}
		return res, ReasonSetModeRejected, err
	}

	s.step(ctx, op, StepDataOn, steps)
	if err := dev.DataSwitch(ctx, true); err != nil {
		return res, ReasonDeviceUnreachable, err
	}
	dataOff = false

	s.step(ctx, op, StepWaitConnect, steps)
	if err := s.pollConn(ctx, dev, device.ConnConnected, s.to.ConnectTimeout); err != nil {
		res.DurationMS = s.elapsedMS(start)
		return res, ReasonNoDataSession, err
	}

	s.step(ctx, op, StepVerify, steps)
	got, err := dev.NetMode(ctx)
	if err != nil {
		res.DurationMS = s.elapsedMS(start)
		return res, ReasonVerifyFailed, err
	}
	if got != want {
		res.DurationMS = s.elapsedMS(start)
		return res, ReasonVerifyFailed, fmt.Errorf("%w: dongle reports net mode %q after the change, want %q", domain.ErrConflict, string(got), string(want))
	}
	res.Changed = true
	if st, err := dev.Status(ctx); err == nil {
		res.ConnStatus = int(st.ConnectionStatus)
	}
	res.DurationMS = s.elapsedMS(start)

	s.step(ctx, op, StepDone, steps)
	return res, "", nil
}

func (s *Service) NetMode(ctx context.Context, dongleID string) (device.NetMode, error) {
	t, err := s.target(ctx, dongleID)
	if err != nil {
		return "", err
	}
	dev, err := s.deps.Dev.ForSlot(ctx, t.slot.Slot)
	if err != nil {
		return "", err
	}
	one, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return dev.NetMode(one)
}
