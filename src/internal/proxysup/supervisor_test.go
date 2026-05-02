package proxysup

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/n4darae/huawei-API/src/internal/config"
	"github.com/n4darae/huawei-API/src/internal/domain"
)

const supervisorSlot = domain.Slot(47)

var (
	fakeBinOnce sync.Once
	fakeBinPath string
	fakeBinErr  error
)

func fake3proxy(t *testing.T) string {
	t.Helper()
	fakeBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "dongled-fake3proxy-")
		if err != nil {
			fakeBinErr = err
			return
		}
		fakeBinPath = filepath.Join(dir, "fake3proxy")
		out, err := exec.Command("go", "build", "-o", fakeBinPath, "../../cmd/fake3proxy").CombinedOutput()
		if err != nil {
			fakeBinErr = errors.New(string(out))
		}
	})
	if fakeBinErr != nil {
		t.Fatalf("build fake3proxy: %v", fakeBinErr)
	}
	return fakeBinPath
}

type fakeRunner struct {
	t            *testing.T
	bin          string
	cfgPath      string
	socks        int
	http         int
	dropOnReload bool

	mu     sync.Mutex
	cmd    *exec.Cmd
	active bool
	calls  []string
}

func (r *fakeRunner) record(v string) {
	r.mu.Lock()
	r.calls = append(r.calls, v)
	r.mu.Unlock()
}

func (r *fakeRunner) Start(ctx context.Context, unit string) error {
	r.record("start")
	return r.spawn()
}

func (r *fakeRunner) Restart(ctx context.Context, unit string) error {
	r.record("restart")
	r.kill()
	return r.spawn()
}

func (r *fakeRunner) Reload(ctx context.Context, unit string) error {
	r.record("reload")
	r.mu.Lock()
	cmd, drop := r.cmd, r.dropOnReload
	r.mu.Unlock()
	if cmd == nil {
		return errors.New("unit is not running")
	}
	if drop {
		if err := cmd.Process.Signal(syscall.SIGUSR1); err != nil {
			return err
		}
		waitClosed(r.socks, r.http)
		return nil
	}
	r.kill()
	return r.spawn()
}

func (r *fakeRunner) Stop(ctx context.Context, unit string) error {
	r.record("stop")
	r.kill()
	r.mu.Lock()
	r.active = false
	r.mu.Unlock()
	return nil
}

func (r *fakeRunner) Disable(ctx context.Context, unit string) error {
	r.record("disable")
	return nil
}

func (r *fakeRunner) Show(ctx context.Context, unit string) (unitState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active {
		return unitState{ActiveState: StateInactive, SubState: "dead"}, nil
	}
	return unitState{ActiveState: StateActive, SubState: "running", MainPID: r.cmd.Process.Pid}, nil
}

func (r *fakeRunner) spawn() error {
	cfg, err := os.ReadFile(r.cfgPath)
	if err != nil {
		return err
	}
	args := []string{
		"-drop-on-reload",
		"-listen", "socks5://127.0.0.1:" + strconv.Itoa(r.socks),
		"-listen", "http://127.0.0.1:" + strconv.Itoa(r.http),
	}
	for name, pass := range ConfigUsers(cfg) {
		args = append(args, "-user", name, "-pass", pass)
		break
	}
	args = append(args, r.cfgPath)

	cmd := exec.Command(r.bin, args...)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()

	r.mu.Lock()
	r.cmd = cmd
	r.active = true
	r.mu.Unlock()
	return waitListening(r.socks, r.http)
}

func (r *fakeRunner) kill() {
	r.mu.Lock()
	cmd := r.cmd
	r.cmd = nil
	r.mu.Unlock()
	if cmd == nil {
		return
	}
	cmd.Process.Kill()
	waitClosed(r.socks, r.http)
}

func waitListening(ports ...int) error {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got, err := listeningPorts(ProcNetTCP, ports)
		if err == nil && len(got) >= len(ports) {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return errors.New("listeners never appeared")
}

func waitClosed(ports ...int) {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got, err := listeningPorts(ProcNetTCP, ports)
		if err == nil && len(got) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func newHarness(t *testing.T) (*sup, *fakeRunner, Spec) {
	t.Helper()
	dir := t.TempDir()
	sp := NewSpec(supervisorSlot, netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("1.1.1.1"))
	sp.ConfigPath = filepath.Join(dir, sp.UserName()+".cfg")
	sp.LogPath = filepath.Join(dir, sp.UserName()+".log")
	sp.Users = []User{{Name: "cust_one", Password: "Kq7mZr2xTn9wLb4V"}}

	r := &fakeRunner{t: t, bin: fake3proxy(t), cfgPath: sp.ConfigPath, socks: sp.SocksPort, http: sp.HTTPPort}
	t.Cleanup(r.kill)

	s, err := NewSystemd(Options{
		Bin:          r.bin,
		ConfDir:      dir,
		LogDir:       dir,
		InternalIP:   netip.MustParseAddr("127.0.0.1"),
		SkipValidate: true,
		ProbeDelay:   10 * time.Millisecond,
		ProbeTimeout: 800 * time.Millisecond,
		runner:       r,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s.(*sup), r, sp
}

func TestApplyStartsAndProbes(t *testing.T) {
	s, r, sp := newHarness(t)
	ctx := context.Background()

	got, err := s.Apply(ctx, sp)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !got.Changed || !got.Restarted || got.Reloaded {
		t.Fatalf("first apply must cold start: %+v", got)
	}
	if !got.Status.Healthy() {
		t.Fatalf("status not healthy: %+v", got.Status)
	}
	if got.ConfigSHA256 == "" {
		t.Fatal("no config digest")
	}

	info, err := os.Stat(sp.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("config mode %v, want 0640", info.Mode().Perm())
	}

	again, err := s.Apply(ctx, sp)
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if again.Changed || again.Reloaded || again.Restarted {
		t.Fatalf("unchanged spec must be a no-op: %+v", again)
	}
	if n := countCalls(r, "restart"); n != 1 {
		t.Fatalf("restart called %d times, want 1", n)
	}
}

func TestApplyReloadsWhenNothingIsRevoked(t *testing.T) {
	s, r, sp := newHarness(t)
	ctx := context.Background()

	if _, err := s.Apply(ctx, sp); err != nil {
		t.Fatal(err)
	}

	sp.Users = append(sp.Users, User{Name: "cust_two", Password: "Aa1Bb2Cc3Dd4Ee5F"})
	got, err := s.Apply(ctx, sp)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !got.Reloaded || got.Restarted {
		t.Fatalf("adding a user must reload, not restart: %+v", got)
	}
	if n := countCalls(r, "reload"); n != 1 {
		t.Fatalf("reload called %d times, want 1", n)
	}
}

func TestApplyRestartsWhenCredentialsAreRevoked(t *testing.T) {
	s, _, sp := newHarness(t)
	ctx := context.Background()

	if _, err := s.Apply(ctx, sp); err != nil {
		t.Fatal(err)
	}

	sp.Users = []User{{Name: "cust_one", Password: "Zz9Yy8Xx7Ww6Vv5U"}}
	got, err := s.Apply(ctx, sp)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !got.Restarted || got.Reloaded {
		t.Fatalf("a rotated password must restart, noforce keeps the old session alive: %+v", got)
	}
	if !got.Status.Healthy() {
		t.Fatalf("status not healthy after restart: %+v", got.Status)
	}
}

func TestApplyRollsBackWhenReloadLeavesZeroListeners(t *testing.T) {
	s, r, sp := newHarness(t)
	ctx := context.Background()

	if _, err := s.Apply(ctx, sp); err != nil {
		t.Fatal(err)
	}
	good, err := os.ReadFile(sp.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}

	r.mu.Lock()
	r.dropOnReload = true
	r.mu.Unlock()

	sp.Users = append(sp.Users, User{Name: "cust_two", Password: "Aa1Bb2Cc3Dd4Ee5F"})
	got, err := s.Apply(ctx, sp)
	if !errors.Is(err, ErrRollback) {
		t.Fatalf("Apply error = %v, want ErrRollback", err)
	}
	if !errors.Is(err, ErrNotBound) {
		t.Fatalf("rollback must name the missing listener: %v", err)
	}

	restored, err := os.ReadFile(sp.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != string(good) {
		t.Fatalf("config was not restored:\n%s", restored)
	}
	if !got.Status.Healthy() {
		t.Fatalf("rollback must leave a healthy proxy: %+v", got.Status)
	}
	if countCalls(r, "restart") < 2 {
		t.Fatalf("rollback must cold start after a failed reload: %v", r.calls)
	}
}

func TestApplyRefusesToInstallAnInvalidSpec(t *testing.T) {
	s, _, sp := newHarness(t)
	sp.Users = nil
	if _, err := s.Apply(context.Background(), sp); !errors.Is(err, ErrNoUsers) {
		t.Fatalf("Apply error = %v, want ErrNoUsers", err)
	}
	if _, err := os.Stat(sp.ConfigPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused render must not touch the config file")
	}
}

func TestStopEvictRemovesTheConfig(t *testing.T) {
	s, r, sp := newHarness(t)
	ctx := context.Background()
	if _, err := s.Apply(ctx, sp); err != nil {
		t.Fatal(err)
	}

	if err := s.Stop(ctx, sp.Slot, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sp.ConfigPath); err != nil {
		t.Fatal("a plain stop must keep the config for a fast restart")
	}
	st, err := s.Status(ctx, sp.Slot)
	if err != nil {
		t.Fatal(err)
	}
	if st.Running || st.Healthy() {
		t.Fatalf("stopped proxy still looks alive: %+v", st)
	}

	if err := s.Stop(ctx, sp.Slot, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sp.ConfigPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("eviction must remove the config")
	}
	if countCalls(r, "disable") != 1 {
		t.Fatalf("eviction must disable the unit: %v", r.calls)
	}
}

func TestStatusRejectsAnInvalidSlot(t *testing.T) {
	s, _, _ := newHarness(t)
	if _, err := s.Status(context.Background(), domain.Slot(0)); !errors.Is(err, ErrBadSlot) {
		t.Fatal("slot 0 must be rejected")
	}
	if err := s.Stop(context.Background(), domain.Slot(999), false); !errors.Is(err, ErrBadSlot) {
		t.Fatal("slot 999 must be rejected")
	}
}

func countCalls(r *fakeRunner, verb string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.calls {
		if c == verb {
			n++
		}
	}
	return n
}

func TestParseUnitShow(t *testing.T) {
	out := "ActiveState=active\nSubState=running\nMainPID=1234\nResult=success\nActiveEnterTimestamp=@1754654400\n"
	st := parseUnitShow(out)
	if st.ActiveState != StateActive || st.SubState != "running" || st.MainPID != 1234 {
		t.Fatalf("%+v", st)
	}
	if st.SinceMS != 1754654400000 {
		t.Fatalf("since %d", st.SinceMS)
	}
}

func TestNewSystemdRequiresAPublicBind(t *testing.T) {
	if _, err := NewSystemd(Options{}); !errors.Is(err, ErrBadAddr) {
		t.Fatalf("err = %v, want ErrBadAddr", err)
	}
}

func TestNewSystemdDefaults(t *testing.T) {
	s, err := NewSystemd(Options{InternalIP: netip.MustParseAddr("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	o := s.(*sup).opt
	if o.Bin != config.Bin3proxy || o.Systemctl != "systemctl" || o.ProcNetTCP != ProcNetTCP {
		t.Fatalf("%+v", o)
	}
	if o.ProbeDelay < 1200*time.Millisecond {
		t.Fatalf("probe delay %s must clear the dual-listener window", o.ProbeDelay)
	}
	if o.ProbeTarget.Port() != config.ProxyValidatePort {
		t.Fatalf("probe target %v", o.ProbeTarget)
	}
}
