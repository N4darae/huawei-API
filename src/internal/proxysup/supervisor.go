package proxysup

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/n4darae/huawei-API/src/internal/config"
	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/fw"
)

const (
	socksVersion    = 0x05
	socksMethodNone = 0x00
	socksMethodUser = 0x02
	socksAuthVer    = 0x01
	socksCmdConnect = 0x01
	socksATypIPv4   = 0x01
	socksRepDenied  = 0x02

	httpStatusForbidden = 403
	httpStatusDenied    = 407

	DefaultProbeDelay   = 1500 * time.Millisecond
	DefaultProbeTimeout = 15 * time.Second
	probeDialTimeout    = 4 * time.Second
	probeRetryInterval  = 250 * time.Millisecond

	ProcNetTCP = "/proc/net/tcp"

	StateActive   = "active"
	StateInactive = "inactive"
	StateFailed   = "failed"
)

var (
	ErrRollback       = errors.New("supervise: apply failed and the previous config was restored")
	ErrPathMismatch   = errors.New("supervise: spec paths must match the directories the unit can read and write")
	ErrProbeGroup     = errors.New("supervise: the probe must not run in the proxy group, nft drops its outbound syn on lo")
	ErrConfUnreadable = errors.New("supervise: the proxy user cannot reopen this config on reload, which fails silently with zero listeners")
)

type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

type unitState struct {
	ActiveState string
	SubState    string
	MainPID     int
	Result      string
	SinceMS     int64
}

type unitRunner interface {
	Start(ctx context.Context, unit string) error
	Stop(ctx context.Context, unit string) error
	Restart(ctx context.Context, unit string) error
	Reload(ctx context.Context, unit string) error
	Disable(ctx context.Context, unit string) error
	Show(ctx context.Context, unit string) (unitState, error)
}

type Options struct {
	Bin             string
	ConfDir         string
	LogDir          string
	InternalIP      netip.Addr
	Systemctl       string
	ProbeTarget     netip.AddrPort
	ProbeDelay      time.Duration
	ProbeTimeout    time.Duration
	ValidateTimeout time.Duration
	SkipValidate    bool
	AllowFallback   bool
	AllowProxyGroup bool
	Dial            DialFunc
	HasListener     func(netip.Addr, int) (bool, error)
	Groups          func() ([]int, error)
	Clock           domain.Clock
	OnValidate      func(ValidateReport)
	runner          unitRunner
}

type sup struct {
	opt Options
}

func NewSystemd(o Options) (Supervisor, error) {
	if !o.InternalIP.IsValid() || !o.InternalIP.Is4() {
		return nil, fmt.Errorf("%w: supervisor needs the node public IPv4", ErrBadAddr)
	}
	if o.Bin == "" {
		o.Bin = config.Bin3proxy
	}
	if o.ConfDir == "" {
		o.ConfDir = config.ProxyConfDir
	}
	if o.LogDir == "" {
		o.LogDir = config.LogDir
	}
	if o.Systemctl == "" {
		o.Systemctl = "systemctl"
	}
	if o.HasListener == nil {
		o.HasListener = fw.HasListener
	}
	if o.Groups == nil {
		o.Groups = processGroups
	}
	if !o.ProbeTarget.IsValid() {
		o.ProbeTarget = netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), config.ProxyValidatePort)
	}
	if o.ProbeDelay <= 0 {
		o.ProbeDelay = DefaultProbeDelay
	}
	if o.ProbeTimeout <= 0 {
		o.ProbeTimeout = DefaultProbeTimeout
	}
	if o.ValidateTimeout <= 0 {
		o.ValidateTimeout = DefaultValidateTimeout
	}
	if o.Dial == nil {
		o.Dial = defaultDial
	}
	if o.Clock == nil {
		o.Clock = domain.SystemClock()
	}
	if o.runner == nil {
		o.runner = systemdRunner{bin: o.Systemctl}
	}
	if !o.AllowProxyGroup {
		if err := refuseProxyGroup(o.Groups); err != nil {
			return nil, err
		}
	}
	return &sup{opt: o}, nil
}

func processGroups() ([]int, error) {
	gids, err := os.Getgroups()
	if err != nil {
		return nil, err
	}
	return append(gids, os.Getegid(), os.Getgid()), nil
}

func refuseProxyGroup(groups func() ([]int, error)) error {
	gids, err := groups()
	if err != nil {
		return err
	}
	for _, g := range gids {
		if g == config.GroupGID {
			return fmt.Errorf("%w: gid %d is in the process group set", ErrProbeGroup, config.GroupGID)
		}
	}
	return nil
}

func (s *sup) configPath(slot domain.Slot) string {
	return filepath.Join(s.opt.ConfDir, slot.UserName()+".cfg")
}

func (s *sup) checkPaths(sp Spec) error {
	if want := s.configPath(sp.Slot); sp.ConfigPath != want {
		return fmt.Errorf("%w: config %q, unit reads %q", ErrPathMismatch, sp.ConfigPath, want)
	}
	if got := filepath.Dir(sp.LogPath); got != s.opt.LogDir {
		return fmt.Errorf("%w: log %q, unit writes %q", ErrPathMismatch, sp.LogPath, s.opt.LogDir)
	}
	return nil
}

func (s *sup) Apply(ctx context.Context, sp Spec) (Applied, error) {
	cfg, err := Render(sp)
	if err != nil {
		return Applied{}, err
	}
	if err := s.checkPaths(sp); err != nil {
		return Applied{}, err
	}

	applied := Applied{
		Slot:         sp.Slot,
		ConfigPath:   sp.ConfigPath,
		ConfigSHA256: sha256hex(cfg),
		AppliedAt:    s.opt.Clock.Now().UnixMilli(),
	}

	old, hadOld := readConfig(sp.ConfigPath)
	before := s.inspect(ctx, sp.Slot, nil)

	if hadOld && bytes.Equal(old, cfg) && before.Healthy() {
		applied.Status = before
		return applied, nil
	}

	if !s.opt.SkipValidate {
		rep, verr := Validate(ctx, ValidateRequest{
			Bin:           s.opt.Bin,
			Config:        cfg,
			Spec:          sp,
			Timeout:       s.opt.ValidateTimeout,
			AllowFallback: s.opt.AllowFallback,
		})
		if s.opt.OnValidate != nil {
			s.opt.OnValidate(rep)
		}
		if verr != nil {
			applied.Status = before
			return applied, verr
		}
	}

	if err := installConfig(sp.ConfigPath, cfg, sp.GID); err != nil {
		applied.Status = before
		return applied, err
	}
	applied.Changed = true

	restart := !before.Running || (hadOld && RevokesAccess(old, cfg))
	if restart {
		applied.Restarted = true
		err = s.opt.runner.Restart(ctx, sp.Unit())
	} else {
		applied.Reloaded = true
		err = s.opt.runner.Reload(ctx, sp.Unit())
	}

	if err == nil {
		var st Status
		st, err = s.await(ctx, sp)
		applied.Status = st
		if err == nil {
			return applied, nil
		}
	}

	rollbackErr := s.rollback(ctx, sp, old, hadOld)
	applied.Status = s.inspect(ctx, sp.Slot, nil)
	applied.Restarted = true
	return applied, fmt.Errorf("%w: %w", ErrRollback, joinErrors(err, rollbackErr))
}

func (s *sup) rollback(ctx context.Context, sp Spec, old []byte, hadOld bool) error {
	if !hadOld {
		return s.opt.runner.Stop(ctx, sp.Unit())
	}
	if err := installConfig(sp.ConfigPath, old, sp.GID); err != nil {
		return err
	}
	if err := s.opt.runner.Reload(ctx, sp.Unit()); err == nil {
		if _, err := s.await(ctx, sp); err == nil {
			return nil
		}
	}
	if err := s.opt.runner.Restart(ctx, sp.Unit()); err != nil {
		return err
	}
	_, err := s.await(ctx, sp)
	return err
}

func (s *sup) Stop(ctx context.Context, slot domain.Slot, evict bool) error {
	if !slot.Valid() {
		return fmt.Errorf("%w: %d", ErrBadSlot, int(slot))
	}
	unit := slot.ProxyUnit()
	if err := s.opt.runner.Stop(ctx, unit); err != nil {
		return err
	}
	if !evict {
		return nil
	}
	if err := s.opt.runner.Disable(ctx, unit); err != nil {
		return err
	}
	if err := os.Remove(s.configPath(slot)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *sup) Status(ctx context.Context, slot domain.Slot) (Status, error) {
	if !slot.Valid() {
		return Status{}, fmt.Errorf("%w: %d", ErrBadSlot, int(slot))
	}
	return s.inspect(ctx, slot, nil), nil
}

func (s *sup) await(ctx context.Context, sp Spec) (Status, error) {
	if err := s.opt.Clock.Sleep(ctx, s.opt.ProbeDelay); err != nil {
		return Status{}, err
	}
	deadline := time.Now().Add(s.opt.ProbeTimeout)
	var st Status
	for {
		st = s.inspect(ctx, sp.Slot, &sp)
		if st.Healthy() {
			return st, nil
		}
		if time.Now().After(deadline) {
			break
		}
		if err := s.opt.Clock.Sleep(ctx, probeRetryInterval); err != nil {
			return st, err
		}
	}
	if !st.SocksBound || !st.HTTPBound {
		return st, fmt.Errorf("%w: %s socks=%v http=%v after %s: %s",
			ErrNotBound, sp.Unit(), st.SocksBound, st.HTTPBound, s.opt.ProbeTimeout, st.Error)
	}
	return st, fmt.Errorf("%w: %s: %s", ErrProbeFailed, sp.Unit(), st.Error)
}

func (s *sup) inspect(ctx context.Context, slot domain.Slot, sp *Spec) Status {
	unit := slot.ProxyUnit()
	st := Status{Unit: unit}

	us, err := s.opt.runner.Show(ctx, unit)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.ActiveState = us.ActiveState
	st.SubState = us.SubState
	st.Since = us.SinceMS
	st.Running = us.ActiveState == StateActive

	socksBound, err := s.opt.HasListener(s.opt.InternalIP, slot.SocksPort())
	if err != nil {
		st.Error = err.Error()
		return st
	}
	httpBound, err := s.opt.HasListener(s.opt.InternalIP, slot.HTTPPort())
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.SocksBound = socksBound
	st.HTTPBound = httpBound
	if !st.SocksBound || !st.HTTPBound {
		st.Error = fmt.Sprintf("listener missing: socks=%v http=%v", st.SocksBound, st.HTTPBound)
		return st
	}

	if err := s.probe(ctx, slot, sp); err != nil {
		st.Error = err.Error()
		return st
	}
	st.ProbeOK = true
	return st
}

func (s *sup) probe(ctx context.Context, slot domain.Slot, sp *Spec) error {
	httpAddr := net.JoinHostPort(s.opt.InternalIP.String(), strconv.Itoa(slot.HTTPPort()))
	socksAddr := net.JoinHostPort(s.opt.InternalIP.String(), strconv.Itoa(slot.SocksPort()))

	if err := probeHTTPDenied(ctx, s.opt.Dial, httpAddr, s.opt.ProbeTarget); err != nil {
		return err
	}
	if sp == nil {
		return probeSocksLive(ctx, s.opt.Dial, socksAddr)
	}
	user, pass := sp.ProbeUser()
	if user == "" {
		return probeSocksLive(ctx, s.opt.Dial, socksAddr)
	}
	if err := probeSocksAuth(ctx, s.opt.Dial, socksAddr, s.opt.ProbeTarget, user, pass); err != nil {
		return err
	}
	return probeHTTPAuth(ctx, s.opt.Dial, httpAddr, s.opt.ProbeTarget, user, pass)
}

func installConfig(path string, cfg []byte, gid int) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".dongled-*.cfg")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)

	if _, err := tmp.Write(cfg); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o640); err != nil {
		tmp.Close()
		return err
	}
	if os.Geteuid() == 0 {
		if err := tmp.Chown(0, gid); err != nil {
			tmp.Close()
			return err
		}
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return err
	}
	return ensureGroupReadable(dir, gid)
}

func ensureGroupReadable(dir string, gid int) error {
	if os.Geteuid() != 0 {
		return nil
	}
	for p := filepath.Clean(dir); ; p = filepath.Dir(p) {
		info, err := os.Stat(p)
		if err != nil {
			return err
		}
		mode := info.Mode().Perm()
		st, ok := info.Sys().(*syscall.Stat_t)
		owned := ok && int(st.Gid) == gid
		switch {
		case mode&0o001 != 0 && mode&0o004 != 0:
		case owned && mode&0o010 != 0 && mode&0o040 != 0:
		default:
			return fmt.Errorf("%w: %s is mode %04o gid %d, the proxy needs traverse and read", ErrConfUnreadable, p, mode, gidOf(st, ok))
		}
		if p == "/" || filepath.Dir(p) == p {
			return nil
		}
	}
}

func gidOf(st *syscall.Stat_t, ok bool) int {
	if !ok {
		return -1
	}
	return int(st.Gid)
}

func readConfig(path string) ([]byte, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return b, true
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

type systemdRunner struct{ bin string }

func (r systemdRunner) Start(ctx context.Context, unit string) error {
	return r.run(ctx, "start", unit)
}

func (r systemdRunner) Stop(ctx context.Context, unit string) error {
	return r.run(ctx, "stop", unit)
}

func (r systemdRunner) Restart(ctx context.Context, unit string) error {
	return r.run(ctx, "restart", unit)
}

func (r systemdRunner) Reload(ctx context.Context, unit string) error {
	return r.run(ctx, "reload", unit)
}

func (r systemdRunner) Disable(ctx context.Context, unit string) error {
	return r.run(ctx, "disable", unit)
}

func (r systemdRunner) run(ctx context.Context, verb, unit string) error {
	cmd := exec.CommandContext(ctx, r.bin, verb, unit)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s %s: %v: %s", verb, unit, err, oneLine(string(out)))
	}
	return nil
}

func (r systemdRunner) Show(ctx context.Context, unit string) (unitState, error) {
	cmd := exec.CommandContext(ctx, r.bin, "show", unit, "--timestamp=unix",
		"--property=ActiveState", "--property=SubState", "--property=MainPID",
		"--property=Result", "--property=ActiveEnterTimestamp")
	out, err := cmd.Output()
	if err != nil {
		return unitState{}, fmt.Errorf("systemctl show %s: %v", unit, err)
	}
	return parseUnitShow(string(out)), nil
}

func parseUnitShow(out string) unitState {
	var st unitState
	for _, l := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), "=")
		if !ok {
			continue
		}
		switch k {
		case "ActiveState":
			st.ActiveState = v
		case "SubState":
			st.SubState = v
		case "Result":
			st.Result = v
		case "MainPID":
			st.MainPID, _ = strconv.Atoi(v)
		case "ActiveEnterTimestamp":
			st.SinceMS = parseUnixStamp(v)
		}
	}
	return st
}

func parseUnixStamp(v string) int64 {
	v = strings.TrimSpace(strings.TrimPrefix(v, "@"))
	if v == "" {
		return 0
	}
	secs, err := strconv.ParseInt(strings.Fields(v)[0], 10, 64)
	if err != nil {
		return 0
	}
	return secs * 1000
}

type UnitOptions struct {
	Bin      string
	ConfDir  string
	LogDir   string
	Group    string
	ExecArgs []string
}

func RenderUnit(o UnitOptions) []byte {
	if o.Bin == "" {
		o.Bin = config.Bin3proxy
	}
	if o.ConfDir == "" {
		o.ConfDir = config.ProxyConfDir
	}
	if o.LogDir == "" {
		o.LogDir = config.LogDir
	}
	if o.Group == "" {
		o.Group = config.GroupName
	}
	exec := o.Bin + " " + o.ConfDir + "/%i.cfg"
	if len(o.ExecArgs) > 0 {
		exec = o.Bin + " " + strings.Join(o.ExecArgs, " ") + " " + o.ConfDir + "/%i.cfg"
	}

	var b bytes.Buffer
	fmt.Fprintf(&b, "[Unit]\n")
	fmt.Fprintf(&b, "Description=%s 3proxy instance %%i\n", config.Product)
	fmt.Fprintf(&b, "After=network-online.target\n")
	fmt.Fprintf(&b, "Wants=network-online.target\n\n")
	fmt.Fprintf(&b, "[Service]\n")
	fmt.Fprintf(&b, "Type=exec\n")
	fmt.Fprintf(&b, "User=%%i\n")
	fmt.Fprintf(&b, "Group=%s\n", o.Group)
	fmt.Fprintf(&b, "ExecStart=%s\n", exec)
	fmt.Fprintf(&b, "ExecReload=/bin/kill -USR1 $MAINPID\n")
	fmt.Fprintf(&b, "Restart=on-failure\n")
	fmt.Fprintf(&b, "RestartSec=2s\n")
	fmt.Fprintf(&b, "KillMode=mixed\n")
	fmt.Fprintf(&b, "KillSignal=SIGTERM\n")
	fmt.Fprintf(&b, "TimeoutStopSec=10s\n")
	fmt.Fprintf(&b, "LimitNOFILE=65536\n")
	fmt.Fprintf(&b, "NoNewPrivileges=yes\n")
	fmt.Fprintf(&b, "PrivateTmp=yes\n")
	fmt.Fprintf(&b, "PrivateDevices=yes\n")
	fmt.Fprintf(&b, "ProtectSystem=strict\n")
	fmt.Fprintf(&b, "ProtectHome=yes\n")
	fmt.Fprintf(&b, "ProtectKernelTunables=yes\n")
	fmt.Fprintf(&b, "ProtectKernelModules=yes\n")
	fmt.Fprintf(&b, "ProtectControlGroups=yes\n")
	fmt.Fprintf(&b, "RestrictAddressFamilies=AF_INET AF_UNIX\n")
	fmt.Fprintf(&b, "RestrictNamespaces=yes\n")
	fmt.Fprintf(&b, "RestrictRealtime=yes\n")
	fmt.Fprintf(&b, "LockPersonality=yes\n")
	fmt.Fprintf(&b, "SystemCallArchitectures=native\n")
	fmt.Fprintf(&b, "ReadWritePaths=%s\n", o.LogDir)
	fmt.Fprintf(&b, "ReadOnlyPaths=%s\n\n", o.ConfDir)
	fmt.Fprintf(&b, "[Install]\n")
	fmt.Fprintf(&b, "WantedBy=multi-user.target\n")
	return b.Bytes()
}

func UnitInstance(unit string) (string, bool) {
	if !strings.HasSuffix(unit, ".service") {
		return "", false
	}
	base := strings.TrimSuffix(unit, ".service")
	prefix, inst, ok := strings.Cut(base, "@")
	if !ok || prefix != config.Product+"-proxy" || inst == "" {
		return "", false
	}
	return inst, true
}

func SlotFromUnit(unit string) (domain.Slot, bool) {
	inst, ok := UnitInstance(unit)
	if !ok || !strings.HasPrefix(inst, domain.UserPrefix) {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(inst, domain.UserPrefix))
	if err != nil {
		return 0, false
	}
	s := domain.Slot(n)
	if !s.Valid() {
		return 0, false
	}
	return s, true
}

func defaultDial(ctx context.Context, network, address string) (net.Conn, error) {
	d := net.Dialer{Timeout: probeDialTimeout}
	return d.DialContext(ctx, network, address)
}

func probeHTTPDenied(ctx context.Context, dial DialFunc, addr string, target netip.AddrPort) error {
	code, err := httpProxyStatus(ctx, dial, addr, target, "", "")
	if err != nil {
		return err
	}
	if code != httpStatusDenied && code != httpStatusForbidden {
		return fmt.Errorf("%w: http proxy %s answered %d to an unauthenticated request", ErrProbeFailed, addr, code)
	}
	return nil
}

func probeHTTPAuth(ctx context.Context, dial DialFunc, addr string, target netip.AddrPort, user, pass string) error {
	code, err := httpProxyStatus(ctx, dial, addr, target, user, pass)
	if err != nil {
		return err
	}
	if code == httpStatusDenied || code == httpStatusForbidden {
		return fmt.Errorf("%w: http proxy %s rejected user %q with %d", ErrProbeFailed, addr, user, code)
	}
	return nil
}

func httpProxyStatus(ctx context.Context, dial DialFunc, addr string, target netip.AddrPort, user, pass string) (int, error) {
	c, err := dial(ctx, "tcp", addr)
	if err != nil {
		return 0, fmt.Errorf("%w: dial %s: %v", ErrProbeFailed, addr, err)
	}
	defer c.Close()
	setDeadline(ctx, c)

	req := "HEAD http://" + target.String() + "/ HTTP/1.0\r\n"
	if user != "" {
		cred := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
		req += "Proxy-Authorization: Basic " + cred + "\r\n"
	}
	req += "\r\n"
	if _, err := c.Write([]byte(req)); err != nil {
		return 0, fmt.Errorf("%w: write %s: %v", ErrProbeFailed, addr, err)
	}

	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil && line == "" {
		return 0, fmt.Errorf("%w: read %s: %v", ErrProbeFailed, addr, err)
	}
	parts := strings.Fields(strings.TrimSpace(line))
	if len(parts) < 2 || !strings.HasPrefix(parts[0], "HTTP/") {
		return 0, fmt.Errorf("%w: %s answered %q", ErrProbeFailed, addr, oneLine(line))
	}
	code, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, fmt.Errorf("%w: %s answered %q", ErrProbeFailed, addr, oneLine(line))
	}
	return code, nil
}

func probeSocksLive(ctx context.Context, dial DialFunc, addr string) error {
	c, err := dial(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("%w: dial %s: %v", ErrProbeFailed, addr, err)
	}
	defer c.Close()
	setDeadline(ctx, c)

	if _, err := c.Write([]byte{socksVersion, 1, socksMethodNone}); err != nil {
		return fmt.Errorf("%w: write %s: %v", ErrProbeFailed, addr, err)
	}
	reply := make([]byte, 2)
	if _, err := readFull(c, reply); err != nil {
		return fmt.Errorf("%w: read %s: %v", ErrProbeFailed, addr, err)
	}
	if reply[0] != socksVersion {
		return fmt.Errorf("%w: %s is not a socks5 listener (first byte %#x)", ErrProbeFailed, addr, reply[0])
	}
	return nil
}

func probeSocksAuth(ctx context.Context, dial DialFunc, addr string, target netip.AddrPort, user, pass string) error {
	c, err := dial(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("%w: dial %s: %v", ErrProbeFailed, addr, err)
	}
	defer c.Close()
	setDeadline(ctx, c)

	if _, err := c.Write([]byte{socksVersion, 2, socksMethodNone, socksMethodUser}); err != nil {
		return fmt.Errorf("%w: write %s: %v", ErrProbeFailed, addr, err)
	}
	reply := make([]byte, 2)
	if _, err := readFull(c, reply); err != nil {
		return fmt.Errorf("%w: read %s: %v", ErrProbeFailed, addr, err)
	}
	if reply[0] != socksVersion {
		return fmt.Errorf("%w: %s is not a socks5 listener (first byte %#x)", ErrProbeFailed, addr, reply[0])
	}
	if reply[1] != socksMethodUser {
		return fmt.Errorf("%w: %s did not select username auth (method %#x)", ErrProbeFailed, addr, reply[1])
	}

	auth := []byte{socksAuthVer, byte(len(user))}
	auth = append(auth, user...)
	auth = append(auth, byte(len(pass)))
	auth = append(auth, pass...)
	if _, err := c.Write(auth); err != nil {
		return fmt.Errorf("%w: write auth %s: %v", ErrProbeFailed, addr, err)
	}
	if _, err := readFull(c, reply); err != nil {
		return fmt.Errorf("%w: read auth %s: %v", ErrProbeFailed, addr, err)
	}

	v4 := target.Addr().As4()
	req := []byte{socksVersion, socksCmdConnect, 0x00, socksATypIPv4}
	req = append(req, v4[:]...)
	req = append(req, byte(target.Port()>>8), byte(target.Port()))
	if _, err := c.Write(req); err != nil {
		return fmt.Errorf("%w: write connect %s: %v", ErrProbeFailed, addr, err)
	}
	head := make([]byte, 4)
	if _, err := readFull(c, head); err != nil {
		return fmt.Errorf("%w: read connect %s: %v", ErrProbeFailed, addr, err)
	}
	if head[1] == socksRepDenied {
		return fmt.Errorf("%w: socks %s denied user %q", ErrProbeFailed, addr, user)
	}
	return nil
}

func readFull(c net.Conn, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		k, err := c.Read(b[n:])
		if k > 0 {
			n += k
		}
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func setDeadline(ctx context.Context, c net.Conn) {
	if d, ok := ctx.Deadline(); ok {
		c.SetDeadline(d)
		return
	}
	c.SetDeadline(time.Now().Add(probeDialTimeout))
}

func joinErrors(errs ...error) error {
	out := make([]error, 0, len(errs))
	for _, e := range errs {
		if e != nil {
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return errors.Join(out...)
}
