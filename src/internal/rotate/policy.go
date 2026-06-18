package rotate

import (
	"fmt"
	"time"

	"github.com/n4darae/huawei-API/src/internal/config"
	"github.com/n4darae/huawei-API/src/internal/domain"
)

type Policy struct {
	HardDeadline       time.Duration
	HoldEscalate       []time.Duration
	WaitConnect        time.Duration
	PollInterval       time.Duration
	VerifyTimeout      time.Duration
	MaxAttempts        int
	RebootBudgetPerDay int
	RebootCooldown     time.Duration
	MinInterval        time.Duration
	MaxConcurrent      int
	Jitter             time.Duration
}

func ProvisionalHoldLadderPendingA3() []time.Duration {
	return cloneLadder(config.Default().Carrier.HoldEscalate)
}

func DefaultPolicy() Policy {
	d := config.Default()
	return Policy{
		HardDeadline:       d.Carrier.HardDeadline,
		HoldEscalate:       cloneLadder(d.Carrier.HoldEscalate),
		WaitConnect:        d.Carrier.WaitConnect,
		PollInterval:       d.Carrier.PollInterval,
		VerifyTimeout:      d.Carrier.VerifyTimeout,
		MaxAttempts:        d.Carrier.MaxAttempts,
		RebootBudgetPerDay: d.Reconcile.RebootBudgetPerDay,
		RebootCooldown:     d.Reconcile.RebootCooldown,
		MinInterval:        d.Carrier.MinRotateInterval,
		MaxConcurrent:      d.Reconcile.MaxConcurrentRotate,
		Jitter:             d.Reconcile.RotateJitter,
	}
}

func cloneLadder(in []time.Duration) []time.Duration {
	if len(in) == 0 {
		return nil
	}
	return append([]time.Duration(nil), in...)
}

func (p Policy) Validate() error {
	if len(p.HoldEscalate) == 0 {
		return fmt.Errorf("%w: rotate policy has an empty hold ladder", domain.ErrInvalid)
	}
	for i, h := range p.HoldEscalate {
		if h <= 0 {
			return fmt.Errorf("%w: rotate hold ladder rung %d is %s", domain.ErrInvalid, i+1, h)
		}
	}
	if p.HardDeadline <= 0 || p.WaitConnect <= 0 || p.VerifyTimeout <= 0 {
		return fmt.Errorf("%w: rotate timeouts must be positive", domain.ErrInvalid)
	}
	if p.PollInterval <= 0 {
		return fmt.Errorf("%w: rotate poll interval must be positive", domain.ErrInvalid)
	}
	if p.MaxAttempts < 1 {
		return fmt.Errorf("%w: rotate needs at least one attempt", domain.ErrInvalid)
	}
	if p.MaxConcurrent < 1 {
		return fmt.Errorf("%w: farm wide rotate cap must be at least one", domain.ErrInvalid)
	}
	if p.Jitter < 0 || p.MinInterval < 0 || p.RebootCooldown < 0 {
		return fmt.Errorf("%w: rotate durations must not be negative", domain.ErrInvalid)
	}
	if p.RebootBudgetPerDay < 0 {
		return fmt.Errorf("%w: reboot budget must not be negative", domain.ErrInvalid)
	}
	return nil
}

type Rung struct {
	Attempt int
	Hold    time.Duration
	Reboot  bool
}

func (p Policy) HoldFor(attempt int) time.Duration {
	if len(p.HoldEscalate) == 0 {
		return 0
	}
	i := attempt - 1
	if i < 0 {
		i = 0
	}
	if i >= len(p.HoldEscalate) {
		i = len(p.HoldEscalate) - 1
	}
	return p.HoldEscalate[i]
}

func (p Policy) Ladder() []Rung {
	out := make([]Rung, 0, p.MaxAttempts+1)
	for a := 1; a <= p.MaxAttempts; a++ {
		out = append(out, Rung{Attempt: a, Hold: p.HoldFor(a)})
	}
	return append(out, p.RebootRung())
}

func (p Policy) RebootRung() Rung {
	return Rung{Attempt: p.MaxAttempts + 1, Reboot: true}
}

func (p Policy) RowDeadline() time.Duration { return p.HardDeadline * 2 }

func (p Policy) AttemptFits(remaining time.Duration, attempt int) bool {
	return remaining >= p.HoldFor(attempt)+p.VerifyTimeout
}

const (
	PctPrecheck    = 5
	PctFence       = 15
	PctDataOff     = 25
	PctHold        = 40
	PctDataOn      = 55
	PctWaitConnect = 70
	PctUnfence     = 85
	PctVerify      = 95
	PctDone        = 100
)

func StepPct(s domain.RotateStep) int {
	switch s {
	case domain.StepPrecheck:
		return PctPrecheck
	case domain.StepFence:
		return PctFence
	case domain.StepDataOff:
		return PctDataOff
	case domain.StepHold:
		return PctHold
	case domain.StepDataOn:
		return PctDataOn
	case domain.StepWaitConnect:
		return PctWaitConnect
	case domain.StepUnfence:
		return PctUnfence
	case domain.StepVerify:
		return PctVerify
	case domain.StepDone:
		return PctDone
	}
	return 0
}

const (
	ReasonChanged           = "changed"
	ReasonUnchanged         = "unchanged"
	ReasonSimLocked         = "sim_locked"
	ReasonEgressLeak        = "egress_leak"
	ReasonDeviceUnreachable = "device_unreachable"
	ReasonNoDataSession     = "no_data_session"
	ReasonSlotEmpty         = "slot_has_no_dongle"
	ReasonFenceFailed       = "fence_failed"
	ReasonProbeFailed       = "probe_failed"
	ReasonDeadline          = "deadline_exceeded"
	ReasonCanceled          = "canceled"
	ReasonProxyDisabled     = "proxy_disabled"
	ReasonSocksFailed       = "socks_probe_failed"
	ReasonHTTPFailed        = "http_probe_failed"
)
