package rotate

import (
	"strings"

	"github.com/n4darae/huawei-API/src/internal/config"
	"github.com/n4darae/huawei-API/src/internal/domain"
)

const DefaultCarrierName = "default"

func CarrierProfileFrom(c config.Carrier) domain.CarrierProfile {
	name := strings.TrimSpace(c.Name)
	if name == "" {
		name = DefaultCarrierName
	}
	return domain.CarrierProfile{
		Name:              name,
		HoldEscalate:      cloneLadder(c.HoldEscalate),
		WaitConnect:       c.WaitConnect,
		VerifyTimeout:     c.VerifyTimeout,
		HardDeadline:      c.HardDeadline,
		PollInterval:      c.PollInterval,
		MaxAttempts:       c.MaxAttempts,
		MinRotateInterval: c.MinRotateInterval,
	}
}

func DefaultCarrierProfile() domain.CarrierProfile {
	return CarrierProfileFrom(config.Default().Carrier)
}

func PolicyForCarrier(c domain.CarrierProfile) Policy {
	p := DefaultPolicy()
	if len(c.HoldEscalate) > 0 {
		p.HoldEscalate = cloneLadder(c.HoldEscalate)
	}
	if c.WaitConnect > 0 {
		p.WaitConnect = c.WaitConnect
	}
	if c.VerifyTimeout > 0 {
		p.VerifyTimeout = c.VerifyTimeout
	}
	if c.HardDeadline > 0 {
		p.HardDeadline = c.HardDeadline
	}
	if c.PollInterval > 0 {
		p.PollInterval = c.PollInterval
	}
	if c.MaxAttempts > 0 {
		p.MaxAttempts = c.MaxAttempts
	}
	if c.MinRotateInterval > 0 {
		p.MinInterval = c.MinRotateInterval
	}
	return p
}

func PolicyForCarrierName(name string, profiles []domain.CarrierProfile) Policy {
	want := strings.ToLower(strings.TrimSpace(name))
	for _, c := range profiles {
		if strings.ToLower(strings.TrimSpace(c.Name)) == want && want != "" {
			return PolicyForCarrier(c)
		}
	}
	return DefaultPolicy()
}
