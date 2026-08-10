package netcfg

import (
	"errors"
	"fmt"
	"net/netip"
	"syscall"
	"testing"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

func TestIsAbsentMapsKernelErrors(t *testing.T) {
	for _, e := range []error{syscall.ENOENT, syscall.ENODEV, syscall.EADDRNOTAVAIL, syscall.ESRCH} {
		if !IsAbsent(e) {
			t.Fatalf("%v must count as absent", e)
		}
		if err := IgnoreAbsent(fmt.Errorf("wrapped: %w", e)); err != nil {
			t.Fatalf("wrapped %v must be ignored, got %v", e, err)
		}
	}
}

func TestIsAbsentReadsCommandOutput(t *testing.T) {
	cases := []string{
		`Cannot find device "dg01"`,
		"RTNETLINK answers: No such file or directory",
		"RTNETLINK answers: No such process",
		"Failed to reconfigure network interface dg09: No such device",
	}
	for _, out := range cases {
		err := &CommandError{Name: "ip", Args: []string{"link"}, Output: out, Err: errors.New("exit status 2")}
		if !IsAbsent(err) {
			t.Fatalf("%q must count as absent", out)
		}
	}
}

func TestIsAbsentDoesNotSwallowRealFailures(t *testing.T) {
	err := &CommandError{
		Name:   "ip",
		Args:   []string{"rule", "add"},
		Output: "RTNETLINK answers: Operation not permitted",
		Err:    errors.New("exit status 2"),
	}
	if IsAbsent(err) {
		t.Fatal("a permission failure must never be mapped to nil")
	}
	if IgnoreAbsent(err) == nil {
		t.Fatal("a permission failure must survive IgnoreAbsent")
	}
	if IsAbsent(nil) {
		t.Fatal("nil is not absent")
	}
}

func TestCommandErrorUnwraps(t *testing.T) {
	base := errors.New("exit status 1")
	err := &CommandError{Name: "networkctl", Args: []string{"reload"}, Output: "boom", Err: base}
	if !errors.Is(err, base) {
		t.Fatal("CommandError must unwrap to the underlying error")
	}
}

func TestValidPublicHost(t *testing.T) {
	good := []string{"139.99.68.39", "10.90.0.1", "2001:db8::1"}
	for _, s := range good {
		if !ValidPublicHost(netip.MustParseAddr(s)) {
			t.Fatalf("%s should be usable as a public host", s)
		}
	}
	bad := []string{"0.0.0.0", "127.0.0.1", "169.254.1.1", "224.0.0.1", "::"}
	for _, s := range bad {
		if ValidPublicHost(netip.MustParseAddr(s)) {
			t.Fatalf("%s must be rejected as a public host", s)
		}
	}
	if ValidPublicHost(netip.Addr{}) {
		t.Fatal("the zero address must be rejected")
	}
}

func TestIsOurRulePriority(t *testing.T) {
	if !IsOurRulePriority(domain.RulePrioPublic) {
		t.Fatal("the global public rule priority is ours")
	}
	for _, s := range domain.Slots() {
		if !IsOurRulePriority(s.RulePrioSrc()) || !IsOurRulePriority(s.RulePrioUID()) {
			t.Fatalf("slot %s priorities must be recognised as ours", s)
		}
	}
	for _, p := range []int{0, 100, 899, 5210, 5270, 32766} {
		if IsOurRulePriority(p) {
			t.Fatalf("priority %d is not ours", p)
		}
	}
}

func TestIsDongleIface(t *testing.T) {
	last := domain.Slot(domain.MaxSlots).IfaceName()
	if !IsDongleIface("dg01") || !IsDongleIface(last) {
		t.Fatal("dongle interfaces must be recognised")
	}
	beyond := fmt.Sprintf("%s%d", domain.IfacePrefix, domain.MaxSlots+1)
	for _, n := range []string{"enp1s0f0", "lo", "tailscale0", "dg00", beyond, "dgx"} {
		if IsDongleIface(n) {
			t.Fatalf("%s must not be treated as a dongle interface", n)
		}
	}
}
