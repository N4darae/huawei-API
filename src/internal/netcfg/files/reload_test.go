package files

import (
	"context"
	"errors"
	"strings"
	"syscall"
	"testing"

	"github.com/n4darae/huawei-API/src/internal/netcfg"
)

func joined(cmds []Command) []string {
	out := make([]string, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, strings.Join(c.Args, " "))
	}
	return out
}

func TestLinkChangeUsesUdevVerbs(t *testing.T) {
	got := joined(Commands("dg01", Changed{Link: true}))
	want := []string{
		"ip link set dg01 down",
		"udevadm control --reload",
		"udevadm trigger --subsystem-match=net --action=add",
		"udevadm settle",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("step %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestNetworkChangeReloadsThenReconfigures(t *testing.T) {
	got := joined(Commands("dg02", Changed{Network: true}))
	want := []string{"networkctl reload", "networkctl reconfigure dg02"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("step %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestNetplanIsNeverInvoked(t *testing.T) {
	all := joined(Commands("dg01", Changed{Link: true, Network: true}))
	for _, c := range all {
		if strings.Contains(c, "netplan") {
			t.Fatalf("netplan restarts networkd and bounces the uplink: %q", c)
		}
		if strings.Contains(c, "systemctl restart") {
			t.Fatalf("reload must never restart a unit: %q", c)
		}
	}
}

func TestNoChangeRunsNothing(t *testing.T) {
	if got := Commands("dg01", Changed{}); len(got) != 0 {
		t.Fatalf("unchanged files must not trigger any command, got %v", joined(got))
	}
}

func TestReloadToleratesMissingInterface(t *testing.T) {
	var seen []string
	r := Reloader{Exec: func(ctx context.Context, name string, args ...string) ([]byte, error) {
		full := strings.Join(append([]string{name}, args...), " ")
		seen = append(seen, full)
		if strings.HasPrefix(full, "ip link set") {
			return []byte(`Cannot find device "dg01"`), &netcfg.CommandError{
				Name: name, Args: args, Output: `Cannot find device "dg01"`, Err: errors.New("exit status 1"),
			}
		}
		if strings.HasPrefix(full, "networkctl reconfigure") {
			return nil, syscall.ENODEV
		}
		return nil, nil
	}}
	if err := r.Apply(context.Background(), "dg01", Changed{Link: true, Network: true}); err != nil {
		t.Fatalf("missing interface must be a no-op, got %v", err)
	}
	if len(seen) != 6 {
		t.Fatalf("every step should still run, got %v", seen)
	}
}

func TestReloadPropagatesRealFailures(t *testing.T) {
	boom := errors.New("networkd is not running")
	r := Reloader{Exec: func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "networkctl" && len(args) > 0 && args[0] == "reload" {
			return nil, boom
		}
		return nil, nil
	}}
	err := r.Apply(context.Background(), "dg01", Changed{Network: true})
	if !errors.Is(err, boom) {
		t.Fatalf("want the underlying failure, got %v", err)
	}
}
