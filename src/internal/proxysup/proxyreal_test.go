//go:build proxyreal

package proxysup

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

const (
	innerEnv   = "DONGLED_PROXYREAL_INNER"
	realSlot   = domain.Slot(1)
	originPort = 18080
	echoPort   = 18081
	internalIP = "139.99.68.39"
	externalIP = "192.168.101.100"
	originIP   = "192.168.101.50"
)

func TestMain(m *testing.M) {
	if os.Getenv(innerEnv) == "1" {
		if err := setupNetns(); err != nil {
			fmt.Fprintln(os.Stderr, "proxyreal:", err)
			os.Exit(1)
		}
		os.Exit(m.Run())
	}
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "proxyreal needs root: sudo -E env PATH=$PATH go test -tags proxyreal ./internal/proxysup/")
		os.Exit(1)
	}
	cmd := exec.Command("/proc/self/exe", os.Args[1:]...)
	cmd.Env = append(os.Environ(), innerEnv+"=1")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWNET}
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		os.Exit(ee.ExitCode())
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "proxyreal:", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func setupNetns() error {
	steps := [][]string{
		{"ip", "link", "set", "lo", "up"},
		{"ip", "addr", "add", internalIP + "/32", "dev", "lo"},
		{"ip", "addr", "add", externalIP + "/24", "dev", "lo"},
		{"ip", "addr", "add", originIP + "/32", "dev", "lo"},
	}
	for _, s := range steps {
		if out, err := exec.Command(s[0], s[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %v: %s", strings.Join(s, " "), err, out)
		}
	}
	return nil
}

func pinnedBin(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("DONGLED_3PROXY_BIN")
	if bin == "" {
		bin = "/usr/local/lib/dongled/3proxy"
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("pinned 3proxy is not at %s: run third_party/3proxy/build.sh", bin)
	}
	return bin
}

func openDir(t *testing.T, dir string) string {
	t.Helper()
	for p := dir; p != "/" && p != "."; p = filepath.Dir(p) {
		if err := os.Chmod(p, 0o755); err != nil {
			t.Fatalf("chmod %s: %v", p, err)
		}
		if err := os.Chown(p, 0, domain.Slot(realSlot).GID()); err != nil {
			t.Fatalf("chown %s: %v", p, err)
		}
		if filepath.Dir(p) == "/tmp" {
			break
		}
	}
	return dir
}

func realSpec(t *testing.T) Spec {
	t.Helper()
	dir := openDir(t, t.TempDir())
	sp := NewSpec(realSlot, netip.MustParseAddr(internalIP), netip.MustParseAddr("1.1.1.1"))
	sp.ConfigPath = filepath.Join(dir, sp.UserName()+".cfg")
	sp.LogPath = filepath.Join(dir, sp.UserName()+".log")
	sp.Users = []User{{Name: "cust_one", Password: "Kq7mZr2xTn9wLb4V"}}
	return sp
}

type proc struct {
	cmd     *exec.Cmd
	outPath string
}

func (p *proc) log() string {
	b, err := os.ReadFile(p.outPath)
	if err != nil {
		return "(no output)"
	}
	return strings.TrimSpace(string(b))
}

func startProxy(t *testing.T, bin, cfg string, wrap ...string) *proc {
	t.Helper()
	f, err := os.CreateTemp(openDir(t, t.TempDir()), "3proxy-out-*")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Chmod(0o666); err != nil {
		t.Fatal(err)
	}

	args := append(append([]string{}, wrap...), bin, cfg)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout, cmd.Stderr = f, f
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", bin, err)
	}
	go cmd.Wait()
	p := &proc{cmd: cmd, outPath: f.Name()}
	t.Cleanup(func() { p.kill() })
	return p
}

func (p *proc) kill() {
	if p.cmd.Process != nil {
		p.cmd.Process.Kill()
	}
}

func (p *proc) alive() bool {
	if p.cmd.Process == nil {
		return false
	}
	return p.cmd.Process.Signal(syscall.Signal(0)) == nil
}

func waitLog(t *testing.T, p *proc, substr string, d time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if strings.Contains(p.log(), substr) {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Logf("output file %s held: %q", p.outPath, p.log())
	return false
}

func (p *proc) reload(t *testing.T) {
	t.Helper()
	if err := p.cmd.Process.Signal(syscall.SIGUSR1); err != nil {
		t.Fatalf("SIGUSR1: %v", err)
	}
}

func waitBound(t *testing.T, addr netip.Addr, ports []int, d time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		n := 0
		for _, p := range ports {
			if c, err := net.DialTimeout("tcp", net.JoinHostPort(addr.String(), strconv.Itoa(p)), 200*time.Millisecond); err == nil {
				c.Close()
				n++
			}
		}
		if n == len(ports) {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return false
}

func waitUnbound(t *testing.T, addr netip.Addr, ports []int, d time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		open := 0
		for _, p := range ports {
			if c, err := net.DialTimeout("tcp", net.JoinHostPort(addr.String(), strconv.Itoa(p)), 200*time.Millisecond); err == nil {
				c.Close()
				open++
			}
		}
		if open == 0 {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return false
}

func writeConfig(t *testing.T, path string, cfg []byte) {
	t.Helper()
	if err := os.WriteFile(path, cfg, 0o644); err != nil {
		t.Fatal(err)
	}
}

func startOrigin(t *testing.T) *sync.Map {
	t.Helper()
	seen := &sync.Map{}
	ln, err := net.Listen("tcp", net.JoinHostPort(originIP, strconv.Itoa(originPort)))
	if err != nil {
		t.Fatalf("origin listen: %v", err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range r.Header {
			seen.Store(strings.ToLower(k), strings.Join(v, ","))
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("origin ok"))
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return seen
}

func startEcho(t *testing.T) {
	t.Helper()
	ln, err := net.Listen("tcp", net.JoinHostPort(originIP, strconv.Itoa(echoPort)))
	if err != nil {
		t.Fatalf("echo listen: %v", err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 256)
				for {
					n, err := c.Read(buf)
					if n > 0 {
						c.Write(buf[:n])
					}
					if err != nil {
						return
					}
				}
			}(c)
		}
	}()
	t.Cleanup(func() { ln.Close() })
}

func httpThroughProxy(t *testing.T, port int, user, pass, target string) (int, string) {
	t.Helper()
	c, err := net.DialTimeout("tcp", net.JoinHostPort(internalIP, strconv.Itoa(port)), 3*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(6 * time.Second))

	req := "GET " + target + " HTTP/1.0\r\nHost: origin\r\n"
	if user != "" {
		req += "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass)) + "\r\n"
	}
	req += "\r\n"
	if _, err := c.Write([]byte(req)); err != nil {
		t.Fatalf("write: %v", err)
	}
	r := bufio.NewReader(c)
	status, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	body := &bytes.Buffer{}
	body.ReadFrom(r)
	fields := strings.Fields(status)
	code := 0
	if len(fields) > 1 {
		code, _ = strconv.Atoi(fields[1])
	}
	return code, body.String()
}

func socksTunnel(t *testing.T, port int, user, pass string, target netip.AddrPort) (net.Conn, byte) {
	t.Helper()
	c, err := net.DialTimeout("tcp", net.JoinHostPort(internalIP, strconv.Itoa(port)), 3*time.Second)
	if err != nil {
		t.Fatalf("dial socks: %v", err)
	}
	c.SetDeadline(time.Now().Add(8 * time.Second))

	if _, err := c.Write([]byte{5, 2, 0, 2}); err != nil {
		t.Fatal(err)
	}
	greet := make([]byte, 2)
	if _, err := readFull(c, greet); err != nil {
		t.Fatal(err)
	}
	if greet[1] == 2 {
		auth := []byte{1, byte(len(user))}
		auth = append(auth, user...)
		auth = append(auth, byte(len(pass)))
		auth = append(auth, pass...)
		if _, err := c.Write(auth); err != nil {
			t.Fatal(err)
		}
		if _, err := readFull(c, greet); err != nil {
			t.Fatal(err)
		}
	}
	v4 := target.Addr().As4()
	req := []byte{5, 1, 0, 1}
	req = append(req, v4[:]...)
	req = append(req, byte(target.Port()>>8), byte(target.Port()))
	if _, err := c.Write(req); err != nil {
		t.Fatal(err)
	}
	head := make([]byte, 10)
	if _, err := readFull(c, head); err != nil {
		t.Fatal(err)
	}
	return c, head[1]
}

func TestRealUnknownDirectiveExitsZeroWithNoListener(t *testing.T) {
	sp := realSpec(t)
	writeConfig(t, sp.ConfigPath, []byte("not_a_command 1\n"))
	p := startProxy(t, pinnedBin(t), sp.ConfigPath)
	if !waitLog(t, p, "Unknown command", 3*time.Second) {
		t.Fatal("a rejected directive must say so on stderr")
	}
	if waitBound(t, sp.InternalIP, sp.Ports(), time.Second) {
		t.Fatal("a rejected directive must open no listener")
	}
}

func TestRealProxyLeaksNoIdentityHeaders(t *testing.T) {
	bin := pinnedBin(t)
	seen := startOrigin(t)
	sp := realSpec(t)

	cfg, err := Render(sp)
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, sp.ConfigPath, cfg)
	p := startProxy(t, bin, sp.ConfigPath)
	if !waitBound(t, sp.InternalIP, sp.Ports(), 3*time.Second) {
		t.Fatalf("no listener: %s", p.log())
	}

	code, body := httpThroughProxy(t, sp.HTTPPort, sp.Users[0].Name, sp.Users[0].Password,
		"http://"+net.JoinHostPort(originIP, strconv.Itoa(originPort))+"/")
	if code != 200 {
		t.Fatalf("proxy returned %d, body %q, log %s", code, body, p.log())
	}
	if !strings.Contains(body, "origin ok") {
		t.Fatalf("body %q", body)
	}
	for _, h := range []string{"via", "x-forwarded-for", "forwarded"} {
		if v, ok := seen.Load(h); ok {
			t.Fatalf("-a leaked %s: %v", h, v)
		}
	}
	if v, ok := seen.Load("proxy-authorization"); ok {
		t.Fatalf("credentials forwarded to the origin: %v", v)
	}
}

func TestRealBadConfigOnReloadLeavesZeroListeners(t *testing.T) {
	bin := pinnedBin(t)
	sp := realSpec(t)

	cfg, err := Render(sp)
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, sp.ConfigPath, cfg)
	p := startProxy(t, bin, sp.ConfigPath)
	if !waitBound(t, sp.InternalIP, sp.Ports(), 3*time.Second) {
		t.Fatalf("no listener: %s", p.log())
	}

	broken := strings.ReplaceAll(string(cfg), "allow cust_one\n",
		"allow cust_one * * 80,443 CONNECT,HTTP_DELETE\n")
	if !strings.Contains(broken, "HTTP_DELETE") {
		t.Fatalf("test fixture did not poison the acl:\n%s", cfg)
	}
	writeConfig(t, sp.ConfigPath, []byte(broken))
	p.reload(t)

	if !waitLog(t, p, "Unknown operation type", 3*time.Second) {
		t.Fatalf("the refused reload left no explanation on stderr, config was:\n%s", broken)
	}
	if bound := waitBound(t, sp.InternalIP, sp.Ports(), time.Second); bound {
		t.Fatal("a rejected reload must leave zero listeners")
	}
	if !p.alive() {
		t.Fatal("the process must stay alive, which is exactly why Restart=on-failure never fires")
	}

	writeConfig(t, sp.ConfigPath, cfg)
	p.reload(t)
	if !waitBound(t, sp.InternalIP, sp.Ports(), 3*time.Second) {
		t.Fatalf("restoring the config must bring the listeners back: %s", p.log())
	}
}

func TestRealApplyRollsBackAStaleInternalBind(t *testing.T) {
	bin := pinnedBin(t)
	sp := realSpec(t)
	dir := filepath.Dir(sp.ConfigPath)

	r := &procRunner{bin: bin, cfg: sp.ConfigPath}
	t.Cleanup(r.stop)

	s, err := NewSystemd(Options{
		Bin:          bin,
		ConfDir:      dir,
		LogDir:       dir,
		InternalIP:   sp.InternalIP,
		SkipValidate: true,
		ProbeDelay:   100 * time.Millisecond,
		ProbeTimeout: 3 * time.Second,
		runner:       r,
	})
	if err != nil {
		t.Fatal(err)
	}
	startEcho(t)

	if _, err := s.Apply(context.Background(), sp); err != nil {
		t.Fatalf("first Apply: %v (%s)", err, r.output())
	}
	good, err := os.ReadFile(sp.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}

	stale := sp
	stale.InternalIP = netip.MustParseAddr("198.51.100.7")
	applied, err := s.Apply(context.Background(), stale)
	if !errors.Is(err, ErrRollback) || !errors.Is(err, ErrNotBound) {
		t.Fatalf("Apply error = %v, want ErrRollback wrapping ErrNotBound", err)
	}
	restored, err := os.ReadFile(sp.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored, good) {
		t.Fatalf("rollback did not restore the previous config:\n%s", restored)
	}
	if !applied.Status.Healthy() {
		t.Fatalf("rollback must end healthy: %+v, log %s", applied.Status, r.output())
	}
}

type procRunner struct {
	bin string
	cfg string

	mu  sync.Mutex
	cmd *exec.Cmd
	out *bytes.Buffer
}

func (r *procRunner) output() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.out == nil {
		return ""
	}
	return r.out.String()
}

func (r *procRunner) Start(ctx context.Context, unit string) error { return r.spawn() }

func (r *procRunner) Restart(ctx context.Context, unit string) error {
	r.stop()
	return r.spawn()
}

func (r *procRunner) Reload(ctx context.Context, unit string) error {
	r.mu.Lock()
	cmd := r.cmd
	r.mu.Unlock()
	if cmd == nil {
		return errors.New("not running")
	}
	return cmd.Process.Signal(syscall.SIGUSR1)
}

func (r *procRunner) Stop(ctx context.Context, unit string) error {
	r.stop()
	return nil
}

func (r *procRunner) Disable(ctx context.Context, unit string) error { return nil }

func (r *procRunner) Show(ctx context.Context, unit string) (unitState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd == nil || r.cmd.Process.Signal(syscall.Signal(0)) != nil {
		return unitState{ActiveState: StateInactive, SubState: "dead"}, nil
	}
	return unitState{ActiveState: StateActive, SubState: "running", MainPID: r.cmd.Process.Pid}, nil
}

func (r *procRunner) spawn() error {
	cmd := exec.Command(r.bin, r.cfg)
	out := &bytes.Buffer{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	r.mu.Lock()
	r.cmd, r.out = cmd, out
	r.mu.Unlock()
	time.Sleep(200 * time.Millisecond)
	return nil
}

func (r *procRunner) stop() {
	r.mu.Lock()
	cmd := r.cmd
	r.cmd = nil
	r.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
		time.Sleep(100 * time.Millisecond)
	}
}

func TestRealNoforceKeepsALiveSessionAcrossReload(t *testing.T) {
	bin := pinnedBin(t)
	startEcho(t)
	sp := realSpec(t)

	cfg, err := Render(sp)
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, sp.ConfigPath, cfg)
	p := startProxy(t, bin, sp.ConfigPath)
	if !waitBound(t, sp.InternalIP, sp.Ports(), 3*time.Second) {
		t.Fatalf("no listener: %s", p.log())
	}

	target := netip.AddrPortFrom(netip.MustParseAddr(originIP), echoPort)
	tunnel, rep := socksTunnel(t, sp.SocksPort, sp.Users[0].Name, sp.Users[0].Password, target)
	defer tunnel.Close()
	if rep != 0 {
		t.Fatalf("socks connect replied %d, log %s", rep, p.log())
	}
	if _, err := tunnel.Write([]byte("first\n")); err != nil {
		t.Fatal(err)
	}
	echo := make([]byte, 6)
	if _, err := readFull(tunnel, echo); err != nil {
		t.Fatal(err)
	}

	revoked := sp
	revoked.Users = []User{{Name: "cust_two", Password: "Aa1Bb2Cc3Dd4Ee5F"}}
	newCfg, err := Render(revoked)
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, sp.ConfigPath, newCfg)
	p.reload(t)
	time.Sleep(1500 * time.Millisecond)

	tunnel.SetDeadline(time.Now().Add(4 * time.Second))
	if _, err := tunnel.Write([]byte("secnd\n")); err != nil {
		t.Fatalf("noforce must keep the live session usable: %v", err)
	}
	if _, err := readFull(tunnel, echo); err != nil {
		t.Fatalf("noforce must keep the live session usable: %v", err)
	}
	if string(echo) != "secnd\n" {
		t.Fatalf("echo %q", echo)
	}

	_, rep = socksTunnel(t, sp.SocksPort, sp.Users[0].Name, sp.Users[0].Password, target)
	if rep != socksRepDenied {
		t.Fatalf("the revoked user must be denied on a NEW session, got reply %d", rep)
	}
}

func TestRealFlushDoesNotAccumulateACLs(t *testing.T) {
	bin := pinnedBin(t)
	startEcho(t)
	sp := realSpec(t)
	target := netip.AddrPortFrom(netip.MustParseAddr(originIP), echoPort)

	first := sp
	first.Users = []User{{Name: "cust_one", Password: "Kq7mZr2xTn9wLb4V"}}
	cfg, err := Render(first)
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, sp.ConfigPath, cfg)
	p := startProxy(t, bin, sp.ConfigPath)
	if !waitBound(t, sp.InternalIP, sp.Ports(), 3*time.Second) {
		t.Fatalf("no listener: %s", p.log())
	}

	c, rep := socksTunnel(t, sp.SocksPort, "cust_one", "Kq7mZr2xTn9wLb4V", target)
	c.Close()
	if rep != 0 {
		t.Fatalf("cust_one must be allowed, reply %d", rep)
	}

	for i := 0; i < 3; i++ {
		second := sp
		second.Users = []User{{Name: "cust_two", Password: "Aa1Bb2Cc3Dd4Ee5F"}}
		newCfg, err := Render(second)
		if err != nil {
			t.Fatal(err)
		}
		writeConfig(t, sp.ConfigPath, newCfg)
		p.reload(t)
		time.Sleep(1500 * time.Millisecond)
		if !waitBound(t, sp.InternalIP, sp.Ports(), 3*time.Second) {
			t.Fatalf("reload %d lost the listeners: %s", i, p.log())
		}

		c, rep := socksTunnel(t, sp.SocksPort, "cust_two", "Aa1Bb2Cc3Dd4Ee5F", target)
		c.Close()
		if rep != 0 {
			t.Fatalf("reload %d: cust_two must be allowed, reply %d", i, rep)
		}
		c, rep = socksTunnel(t, sp.SocksPort, "cust_one", "Kq7mZr2xTn9wLb4V", target)
		c.Close()
		if rep != socksRepDenied {
			t.Fatalf("reload %d: flush did not drop the old acl, cust_one still allowed (reply %d)", i, rep)
		}

		writeConfig(t, sp.ConfigPath, cfg)
		p.reload(t)
		time.Sleep(1500 * time.Millisecond)
	}
}

func TestRealSetgidFailureProducesZeroListeners(t *testing.T) {
	bin := pinnedBin(t)
	if _, err := exec.LookPath("setpriv"); err != nil {
		t.Skip("setpriv is not installed")
	}
	sp := realSpec(t)
	cfg, err := Render(sp)
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, sp.ConfigPath, cfg)
	dir := filepath.Dir(sp.ConfigPath)
	if err := os.Chown(dir, 65534, 65534); err != nil {
		t.Fatal(err)
	}

	p := startProxy(t, bin, sp.ConfigPath,
		"setpriv", "--reuid=65534", "--regid=65534", "--clear-groups", "--")
	time.Sleep(1500 * time.Millisecond)

	for _, port := range sp.Ports() {
		if c, err := net.DialTimeout("tcp", net.JoinHostPort(internalIP, strconv.Itoa(port)), 300*time.Millisecond); err == nil {
			c.Close()
			t.Fatalf("port %d bound despite a setgid it cannot perform", port)
		}
	}
	if !waitLog(t, p, "Unable to set", 3*time.Second) {
		t.Fatal("expected a privilege drop failure on stderr")
	}
	t.Logf("setgid failure output: %s", p.log())
}

func TestRealValidateInNetnsAcceptsTheExactBytes(t *testing.T) {
	bin := pinnedBin(t)
	sp := realSpec(t)
	cfg, err := Render(sp)
	if err != nil {
		t.Fatal(err)
	}

	rep, err := Validate(context.Background(), ValidateRequest{
		Bin:     bin,
		Config:  cfg,
		Spec:    sp,
		Timeout: 4 * time.Second,
	})
	if err != nil {
		t.Fatalf("Validate: %v (%s)", err, rep.Stderr)
	}
	if rep.Mode != ValidateNetns || rep.Degraded {
		t.Fatalf("netns validation must not be degraded: %+v", rep)
	}
	if len(rep.BoundPorts) < 2 {
		t.Fatalf("bound %v, want both service ports", rep.BoundPorts)
	}

	if c, err := net.DialTimeout("tcp", net.JoinHostPort(internalIP, strconv.Itoa(sp.SocksPort)), 300*time.Millisecond); err == nil {
		c.Close()
		t.Fatal("validation must not bind on the real host ports")
	}
}

func TestRealValidateInNetnsRejectsAnUnknownKeyword(t *testing.T) {
	bin := pinnedBin(t)
	sp := realSpec(t)
	cfg, err := Render(sp)
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(cfg), "allow cust_one\n",
		"allow cust_one * * 80,443 CONNECT,HTTP_DELETE\n", 1)

	rep, err := Validate(context.Background(), ValidateRequest{
		Bin:     bin,
		Config:  []byte(broken),
		Spec:    sp,
		Timeout: 4 * time.Second,
	})
	if !errors.Is(err, ErrValidateFailed) {
		t.Fatalf("err = %v, want ErrValidateFailed", err)
	}
	if !strings.Contains(rep.Stderr, "Unknown operation type") {
		t.Fatalf("stderr %q", rep.Stderr)
	}
}

func TestRealTimeoutsLineIsAccepted(t *testing.T) {
	bin := pinnedBin(t)
	sp := realSpec(t)
	cfg, err := Render(sp)
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, sp.ConfigPath, cfg)
	p := startProxy(t, bin, sp.ConfigPath)
	if !waitBound(t, sp.InternalIP, sp.Ports(), 3*time.Second) {
		t.Fatalf("the frozen timeouts line was rejected: %s", p.log())
	}
	if strings.Contains(p.log(), "wrong number of arguments") {
		t.Fatalf("timeouts arity rejected: %s", p.log())
	}
}
