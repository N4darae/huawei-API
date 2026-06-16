package devops

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/domain"
)

const (
	DHCPPoolStartOctet = 100
	DHCPPoolEndOctet   = 200
)

type LanIPResult struct {
	From         string `json:"from"`
	To           string `json:"to"`
	Moved        bool   `json:"moved"`
	PostTimedOut bool   `json:"post_timed_out"`
	Rediscovered bool   `json:"rediscovered"`
	Supported    bool   `json:"lan_ip_change_supported"`
	DurationMS   int    `json:"duration_ms"`
	Note         string `json:"note,omitempty"`
}

func (s *Service) SetLanIP(ctx context.Context, dongleID string, gw netip.Addr) (*domain.Operation, error) {
	return s.SetLanIPAs(ctx, dongleID, gw, AdminActor())
}

func (s *Service) SetLanIPAs(ctx context.Context, dongleID string, gw netip.Addr, a Actor) (*domain.Operation, error) {
	t, err := s.target(ctx, dongleID)
	if err != nil {
		return nil, err
	}
	if !gw.IsValid() || !gw.Is4() {
		return nil, fmt.Errorf("%w: lan gateway %v", domain.ErrInvalid, gw)
	}
	if gw != t.slot.Slot.GatewayIP() {
		return nil, fmt.Errorf("%w: slot %d is planned for %s, not %s", ErrLanIPNotPlanned, t.slot.Slot.Int(), t.slot.Slot.GatewayIP(), gw)
	}
	if s.deps.Net == nil {
		return nil, ErrNoNetcfg
	}
	if err := s.checkConflict(ctx, domain.SubjectDongle, dongleID); err != nil {
		return nil, err
	}
	if err := s.checkRotateFence(ctx, t); err != nil {
		return nil, err
	}
	return s.start(ctx, domain.OpSetLanIP, domain.SubjectDongle, dongleID, a, s.to.LanIPDeadline,
		func(ctx context.Context, op *domain.Operation) (any, string, error) {
			return s.runLanIP(ctx, op, t, gw)
		})
}

func (s *Service) runLanIP(ctx context.Context, op *domain.Operation, t dongleTarget, gw netip.Addr) (any, string, error) {
	start := s.deps.Clock.Now()
	steps := LanIPSteps()
	res := LanIPResult{To: gw.String()}

	s.step(ctx, op, StepPrecheck, steps)
	from, cur, err := s.locate(ctx, gw)
	if err != nil {
		res.DurationMS = s.elapsedMS(start)
		return res, ReasonDeviceUnreachable, err
	}
	res.From = from.String()
	if cur.DHCPIPAddress == gw {
		res.Supported = true
		res.Rediscovered = true
		res.DurationMS = s.elapsedMS(start)
		_ = s.deps.Repos.Dongles().SetCapabilities(ctx, t.dongle.ID, true, t.dongle.HilinkLoginRequired)
		s.step(ctx, op, StepDone, steps)
		return res, "", nil
	}

	s.step(ctx, op, StepWriteNetcfg, steps)
	if err := s.deps.Net.ApplySlot(ctx, t.slot.Slot, t.slot.IDPath, ""); err != nil {
		res.DurationMS = s.elapsedMS(start)
		return res, ReasonNetcfgFailed, err
	}

	s.step(ctx, op, StepPostDHCP, steps)
	dev, err := s.deps.Dev.ForAddr(ctx, from)
	if err != nil {
		res.DurationMS = s.elapsedMS(start)
		return res, ReasonDeviceUnreachable, err
	}
	if err := dev.SetDHCPSettings(ctx, moveDHCP(cur, gw)); err != nil {
		switch {
		case errors.Is(err, domain.ErrUnsupported), errors.Is(err, domain.ErrSystemBusy):
			_ = s.deps.Repos.Dongles().SetCapabilities(ctx, t.dongle.ID, false, t.dongle.HilinkLoginRequired)
			res.DurationMS = s.elapsedMS(start)
			return res, ReasonLanIPUnsupported, fmt.Errorf("%w: %v", ErrLanIPUnsupported, err)
		case errors.Is(err, domain.ErrUnreachable), errors.Is(err, context.DeadlineExceeded):
			res.PostTimedOut = true
			res.Note = "the dhcp post never answered, which is the normal shape of a successful move"
		default:
			res.DurationMS = s.elapsedMS(start)
			return res, ReasonDeviceUnreachable, err
		}
	}

	s.step(ctx, op, StepRediscovering, steps)
	moved, err := s.rediscover(ctx, gw)
	if err != nil {
		_ = s.deps.Repos.Dongles().SetCapabilities(ctx, t.dongle.ID, false, t.dongle.HilinkLoginRequired)
		res.DurationMS = s.elapsedMS(start)
		return res, ReasonLanIPNotFound, err
	}
	res.Rediscovered = true

	s.step(ctx, op, StepVerify, steps)
	got, err := moved.DHCPSettings(ctx)
	if err != nil {
		res.DurationMS = s.elapsedMS(start)
		return res, ReasonVerifyFailed, err
	}
	if got.DHCPIPAddress != gw {
		res.DurationMS = s.elapsedMS(start)
		return res, ReasonVerifyFailed, fmt.Errorf("%w: the dongle answers at %s but reports lan address %s", domain.ErrConflict, gw, got.DHCPIPAddress)
	}
	res.Moved = true
	res.Supported = true
	res.DurationMS = s.elapsedMS(start)
	if err := s.deps.Repos.Dongles().SetCapabilities(ctx, t.dongle.ID, true, t.dongle.HilinkLoginRequired); err != nil {
		res.Note = joinNote(res.Note, "capability flag not stored: "+err.Error())
	}

	s.step(ctx, op, StepDone, steps)
	return res, "", nil
}

func (s *Service) locate(ctx context.Context, gw netip.Addr) (netip.Addr, device.DHCPSettings, error) {
	candidates := []netip.Addr{gw, device.FactoryDefaultAddr}
	var last error
	for _, addr := range candidates {
		if !addr.IsValid() {
			continue
		}
		dev, err := s.deps.Dev.ForAddr(ctx, addr)
		if err != nil {
			last = err
			continue
		}
		if !dev.Reachable(ctx) {
			last = fmt.Errorf("%w: nothing answers at %s", domain.ErrUnreachable, addr)
			continue
		}
		cur, err := dev.DHCPSettings(ctx)
		if err != nil {
			last = err
			continue
		}
		return addr, cur, nil
	}
	if last == nil {
		last = domain.ErrUnreachable
	}
	return netip.Addr{}, device.DHCPSettings{}, last
}

func (s *Service) rediscover(ctx context.Context, gw netip.Addr) (device.Device, error) {
	deadline := s.deps.Clock.Now().Add(s.to.RediscoverWindow)
	var last error
	for {
		dev, err := s.deps.Dev.ForAddr(ctx, gw)
		if err == nil && dev.Reachable(ctx) {
			return dev, nil
		}
		if err != nil {
			last = err
		}
		if !s.deps.Clock.Now().Before(deadline) {
			if last == nil {
				last = fmt.Errorf("%w: the dongle never answered at %s within %s", domain.ErrUnreachable, gw, s.to.RediscoverWindow)
			}
			return nil, last
		}
		if err := s.deps.Clock.Sleep(ctx, s.to.PollInterval); err != nil {
			return nil, err
		}
	}
}

func moveDHCP(cur device.DHCPSettings, gw netip.Addr) device.DHCPSettings {
	out := cur
	out.DHCPIPAddress = gw
	out.DHCPStartIPAddress = withLastOctet(gw, DHCPPoolStartOctet)
	out.DHCPEndIPAddress = withLastOctet(gw, DHCPPoolEndOctet)
	out.DHCPLanNetmask = netip.AddrFrom4([4]byte{255, 255, 255, 0})
	out.DNSStatus = true
	out.PrimaryDNS = gw
	out.SecondaryDNS = gw
	if out.DHCPLeaseTime <= 0 {
		out.DHCPLeaseTime = 86400
	}
	return out
}

func withLastOctet(a netip.Addr, last byte) netip.Addr {
	if !a.Is4() {
		return a
	}
	b := a.As4()
	b[3] = last
	return netip.AddrFrom4(b)
}

func joinNote(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + "; " + b
	}
}
