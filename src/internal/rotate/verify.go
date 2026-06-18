package rotate

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

var ErrProbeEgressLeak = errors.New("rotate: probe egressed via the host uplink, routing is broken")

func CheckLeak(observed, publicHost netip.Addr) error {
	if !observed.IsValid() || !publicHost.IsValid() {
		return nil
	}
	if observed.Unmap() != publicHost.Unmap() {
		return nil
	}
	return fmt.Errorf("%w: probe observed %s, which is the node public host", ErrProbeEgressLeak, observed)
}

type Verdict struct {
	Old       netip.Addr
	New       netip.Addr
	Changed   bool
	LatencyMS int
	Echo      string
}

type SelftestResult struct {
	SocksOK   bool
	HTTPOK    bool
	EgressIP  netip.Addr
	LatencyMS int
	Error     string
}

func (r SelftestResult) OK() bool { return r.SocksOK && r.HTTPOK && r.Error == "" }

func (e *Engine) probeEgress(ctx context.Context, src, publicHost netip.Addr) (EgressProbe, error) {
	if e.deps.Probe == nil {
		return EgressProbe{}, ErrProbeUnavailable
	}
	one, cancel := context.WithTimeout(ctx, e.pol.VerifyTimeout)
	defer cancel()
	p, err := e.deps.Probe.ProbeSource(one, src)
	if err != nil {
		return EgressProbe{}, err
	}
	if err := CheckLeak(p.IP, publicHost); err != nil {
		return p, err
	}
	return p, nil
}

func (e *Engine) Selftest(ctx context.Context, proxyID string) (SelftestResult, error) {
	t, err := e.target(ctx, proxyID)
	if err != nil {
		return SelftestResult{}, err
	}
	if e.deps.Probe == nil {
		return SelftestResult{}, ErrProbeUnavailable
	}

	host := t.node.PublicHost
	if !host.IsValid() {
		return SelftestResult{}, fmt.Errorf("%w: node %q has no public host", domain.ErrInvalid, t.node.ID)
	}

	socksEP := Endpoint{Addr: netip.AddrPortFrom(host, uint16(t.proxy.SocksPort))}
	httpEP := Endpoint{Addr: netip.AddrPortFrom(host, uint16(t.proxy.HTTPPort))}
	if t.proxy.AuthMode.UsesUserPass() {
		socksEP.User, socksEP.Pass = t.proxy.Username, t.proxy.Password
		httpEP.User, httpEP.Pass = t.proxy.Username, t.proxy.Password
	}

	out := SelftestResult{}
	socks, socksErr := e.deps.Probe.ProbeSocks(ctx, socksEP)
	if socksErr == nil {
		out.SocksOK = true
		out.EgressIP = socks.IP
		out.LatencyMS = socks.LatencyMS
	}
	httpProbe, httpErr := e.deps.Probe.ProbeHTTP(ctx, httpEP)
	if httpErr == nil {
		out.HTTPOK = true
		if !out.EgressIP.IsValid() {
			out.EgressIP = httpProbe.IP
			out.LatencyMS = httpProbe.LatencyMS
		}
	}

	if err := CheckLeak(out.EgressIP, host); err != nil {
		out.Error = ReasonEgressLeak
		return out, nil
	}
	switch {
	case socksErr != nil && httpErr != nil:
		out.Error = ReasonProbeFailed
	case socksErr != nil:
		out.Error = ReasonSocksFailed
	case httpErr != nil:
		out.Error = ReasonHTTPFailed
	}
	return out, nil
}
