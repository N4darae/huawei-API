//go:build netns

package netns

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/fw"
)

const (
	Prefix       = "dgl-t"
	HostNS       = Prefix + "-host"
	CustNS       = Prefix + "-cust"
	PublicIf     = "pub0"
	CustIf       = "cust0"
	DongleIf     = "dgw0"
	InnerEnv     = "DONGLED_NETNS_INNER"
	EnvResponder = "DONGLED_NETNS_RESPONDER"
	StateEnv     = "DONGLED_NETNS_STATE"
)

var (
	PublicIP   = netip.MustParseAddr("10.90.0.1")
	CustIP     = netip.MustParseAddr("10.90.0.2")
	WebTarget  = netip.MustParseAddr("8.8.8.8")
	DNSServer  = netip.MustParseAddr("1.1.1.1")
	LocalDecoy = netip.MustParseAddr("203.0.113.9")

	ErrNotRoot     = errors.New("netns: the suite needs root and must never fall back to the fake backends")
	ErrRootNetns   = errors.New("netns: refusing to touch the root network namespace")
	ErrNoNftBinary = errors.New("netns: nft is not installed")
)

func DongleNS(s domain.Slot) string { return fmt.Sprintf("%s-dg%02d", Prefix, int(s)) }

func Run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func RunIn(ctx context.Context, ns string, args ...string) ([]byte, error) {
	if ns == "" {
		return Run(ctx, args...)
	}
	return Run(ctx, append([]string{"ip", "netns", "exec", ns}, args...)...)
}

func ipCmd(ns string, args ...string) []string {
	if ns == "" {
		return append([]string{"ip"}, args...)
	}
	return append([]string{"ip", "-n", ns}, args...)
}

func MustRun(ctx context.Context, args ...string) error {
	_, err := Run(ctx, args...)
	return err
}

func RequireRoot() error {
	if os.Geteuid() != 0 {
		return ErrNotRoot
	}
	return nil
}

func RefuseRootNetns() error {
	self, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		return err
	}
	init, err := os.Readlink("/proc/1/ns/net")
	if err != nil {
		return nil
	}
	if self == init {
		return fmt.Errorf("%w: %s", ErrRootNetns, self)
	}
	return nil
}

func InRootNetns() bool { return RefuseRootNetns() != nil }

type Topology struct {
	Slots   []domain.Slot
	StateFS string
	nsNames []string
	procs   []*exec.Cmd
	mu      sync.Mutex
}

func NewTopology(slots []domain.Slot, stateDir string) *Topology {
	return &Topology{Slots: slots, StateFS: stateDir}
}

func (t *Topology) namespaces() []string {
	out := []string{HostNS, CustNS}
	for _, s := range t.Slots {
		out = append(out, DongleNS(s))
	}
	return out
}

func (t *Topology) Build(ctx context.Context) error {
	if err := RequireRoot(); err != nil {
		return err
	}
	if _, err := exec.LookPath("nft"); err != nil {
		return ErrNoNftBinary
	}
	t.Destroy(ctx)
	for _, ns := range t.namespaces() {
		if err := MustRun(ctx, "ip", "netns", "add", ns); err != nil {
			return err
		}
		t.nsNames = append(t.nsNames, ns)
		if err := MustRun(ctx, "ip", "-n", ns, "link", "set", "lo", "up"); err != nil {
			return err
		}
	}
	if err := t.buildPublicLeg(ctx); err != nil {
		return err
	}
	for _, s := range t.Slots {
		if err := t.buildDongleLeg(ctx, s); err != nil {
			return err
		}
	}
	for _, sysctl := range []string{
		"net.ipv4.conf.all.rp_filter=2",
		"net.ipv4.conf.default.rp_filter=2",
		"net.ipv4.ip_forward=0",
	} {
		if err := MustRun(ctx, "ip", "netns", "exec", HostNS, "sysctl", "-qw", sysctl); err != nil {
			return err
		}
	}
	return nil
}

func (t *Topology) buildPublicLeg(ctx context.Context) error {
	steps := [][]string{
		{"ip", "link", "add", PublicIf, "netns", HostNS, "type", "veth", "peer", "name", CustIf, "netns", CustNS},
		{"ip", "-n", HostNS, "addr", "add", PublicIP.String() + "/24", "dev", PublicIf},
		{"ip", "-n", HostNS, "link", "set", PublicIf, "up"},
		{"ip", "-n", HostNS, "addr", "add", LocalDecoy.String() + "/32", "dev", "lo"},
		{"ip", "-n", CustNS, "addr", "add", CustIP.String() + "/24", "dev", CustIf},
		{"ip", "-n", CustNS, "link", "set", CustIf, "up"},
		{"ip", "-n", CustNS, "addr", "add", WebTarget.String() + "/32", "dev", "lo"},
		{"ip", "-n", CustNS, "addr", "add", DNSServer.String() + "/32", "dev", "lo"},
		{"ip", "-n", CustNS, "route", "replace", "10.90.0.0/24", "dev", CustIf},
		{"ip", "-n", HostNS, "route", "replace", "default", "via", CustIP.String(), "dev", PublicIf},
	}
	for _, s := range steps {
		if err := MustRun(ctx, s...); err != nil {
			return err
		}
	}
	return nil
}

func (t *Topology) buildDongleLeg(ctx context.Context, s domain.Slot) error {
	ns := DongleNS(s)
	iface := s.IfaceName()
	steps := [][]string{
		{"ip", "link", "add", iface, "netns", HostNS, "type", "veth", "peer", "name", DongleIf, "netns", ns},
		{"ip", "-n", ns, "addr", "add", s.GatewayIP().String() + "/24", "dev", DongleIf},
		{"ip", "-n", ns, "link", "set", DongleIf, "up"},
		{"ip", "-n", ns, "addr", "add", WebTarget.String() + "/32", "dev", "lo"},
		{"ip", "-n", ns, "addr", "add", DNSServer.String() + "/32", "dev", "lo"},
		{"ip", "-n", ns, "route", "replace", s.Subnet().String(), "dev", DongleIf},
	}
	for _, step := range steps {
		if err := MustRun(ctx, step...); err != nil {
			return err
		}
	}
	return nil
}

func (t *Topology) StartResponders(ctx context.Context, self string) error {
	if err := os.MkdirAll(t.StateFS, 0o777); err != nil {
		return err
	}
	if err := os.Chmod(t.StateFS, 0o777); err != nil {
		return err
	}
	specs := []struct {
		ns  string
		tag string
	}{{CustNS, "public-leak"}}
	for _, s := range t.Slots {
		specs = append(specs, struct {
			ns  string
			tag string
		}{DongleNS(s), fmt.Sprintf("dongle-%02d", int(s))})
	}
	for _, spec := range specs {
		cmd := exec.Command("ip", "netns", "exec", spec.ns, self, "-test.run", "TestNetnsResponder", "-test.timeout", "30m")
		cmd.Env = append(os.Environ(),
			ResponderEnv(spec.tag),
			"DONGLED_NETNS_WEB="+WebTarget.String(),
			"DONGLED_NETNS_DNS="+DNSServer.String(),
			StateEnv+"="+t.StateFS,
		)
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Start(); err != nil {
			return err
		}
		t.mu.Lock()
		t.procs = append(t.procs, cmd)
		t.mu.Unlock()
	}
	return t.waitForResponders(ctx)
}

func ResponderEnv(tag string) string { return EnvResponder + "=" + tag }

func (t *Topology) waitForResponders(ctx context.Context) error {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		ok := true
		for _, s := range t.Slots {
			if _, err := RunIn(ctx, DongleNS(s), "curl", "-sS", "--max-time", "2", "http://"+WebTarget.String()+"/ping"); err != nil {
				ok = false
			}
		}
		if _, err := RunIn(ctx, CustNS, "curl", "-sS", "--max-time", "2", "http://"+WebTarget.String()+"/ping"); err != nil {
			ok = false
		}
		if ok {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return errors.New("netns: responders did not come up")
}

func (t *Topology) Destroy(ctx context.Context) {
	t.mu.Lock()
	procs := t.procs
	t.procs = nil
	t.mu.Unlock()
	for _, p := range procs {
		if p.Process != nil {
			p.Process.Kill()
			p.Wait()
		}
	}
	for _, ns := range t.namespaces() {
		exec.Command("ip", "netns", "del", ns).Run()
	}
	t.nsNames = nil
}

func CleanupAll(ctx context.Context) {
	out, err := Run(ctx, "ip", "netns", "list")
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(out), "\n") {
		name := strings.Fields(line)
		if len(name) == 0 || !strings.HasPrefix(name[0], Prefix) {
			continue
		}
		exec.Command("ip", "netns", "del", name[0]).Run()
	}
}

type IniFile struct {
	Sections []IniSection
}

type IniSection struct {
	Name string
	Keys [][2]string
}

func ParseIni(body []byte) IniFile {
	var out IniFile
	var cur *IniSection
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			out.Sections = append(out.Sections, IniSection{Name: strings.Trim(line, "[]")})
			cur = &out.Sections[len(out.Sections)-1]
			continue
		}
		if cur == nil {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		cur.Keys = append(cur.Keys, [2]string{strings.TrimSpace(k), strings.TrimSpace(v)})
	}
	return out
}

func (f IniFile) Get(section, key string) string {
	for _, s := range f.Sections {
		if s.Name != section {
			continue
		}
		for _, kv := range s.Keys {
			if kv[0] == key {
				return kv[1]
			}
		}
	}
	return ""
}

func (f IniFile) Each(section string, fn func(IniSection)) {
	for _, s := range f.Sections {
		if s.Name == section {
			fn(s)
		}
	}
}

func (s IniSection) Get(key string) string {
	for _, kv := range s.Keys {
		if kv[0] == key {
			return kv[1]
		}
	}
	return ""
}

func ApplyNetworkFile(ctx context.Context, ns string, body []byte) error {
	f := ParseIni(body)
	iface := f.Get("Match", "Name")
	if iface == "" {
		return errors.New("netns: rendered network file has no Match Name")
	}
	if addr := f.Get("Network", "Address"); addr != "" {
		if err := ignoreExists(MustRun(ctx, ipCmd(ns, "addr", "add", addr, "dev", iface)...)); err != nil {
			return err
		}
	}
	if err := MustRun(ctx, ipCmd(ns, "link", "set", iface, "up")...); err != nil {
		return err
	}
	var routeErr error
	f.Each("Route", func(s IniSection) {
		if routeErr != nil {
			return
		}
		dst := s.Get("Destination")
		table := s.Get("Table")
		args := ipCmd(ns, "route", "replace", dst)
		if gw := s.Get("Gateway"); gw != "" {
			args = append(args, "via", gw)
		}
		if s.Get("Scope") == "link" {
			args = append(args, "scope", "link")
		}
		args = append(args, "dev", iface)
		if table != "" {
			args = append(args, "table", table)
		}
		routeErr = MustRun(ctx, args...)
	})
	if routeErr != nil {
		return routeErr
	}
	var ruleErr error
	f.Each("RoutingPolicyRule", func(s IniSection) {
		if ruleErr != nil {
			return
		}
		args := ipCmd(ns, "rule", "add")
		if from := s.Get("From"); from != "" {
			args = append(args, "from", from)
		}
		if user := s.Get("User"); user != "" {
			args = append(args, "uidrange", user)
		}
		args = append(args, "lookup", s.Get("Table"), "priority", s.Get("Priority"))
		ruleErr = ignoreExists(MustRun(ctx, args...))
	})
	return ruleErr
}

func ignoreExists(err error) error {
	if err == nil {
		return nil
	}
	low := strings.ToLower(err.Error())
	if strings.Contains(low, "file exists") || strings.Contains(low, "object exists") {
		return nil
	}
	return err
}

func RulePriorities(ctx context.Context, ns string) ([]int, error) {
	out, err := RunIn(ctx, ns, "ip", "rule", "show")
	if err != nil {
		return nil, err
	}
	var prios []int
	for _, line := range strings.Split(string(out), "\n") {
		head, _, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		p, err := strconv.Atoi(head)
		if err != nil {
			continue
		}
		prios = append(prios, p)
	}
	return prios, nil
}

type ProxyConfig struct {
	Slot       domain.Slot
	InternalIP netip.Addr
	ExternalIP netip.Addr
	NServer    netip.Addr
	User       string
	Password   string
	LogPath    string
	UID        int
	GID        int
	NoCache    bool
}

func (c ProxyConfig) Render() string {
	var b strings.Builder
	if !c.NoCache {
		b.WriteString("nscache 65536\n")
	}
	fmt.Fprintf(&b, "nserver %s\n", c.NServer)
	b.WriteString("timeouts 1 5 30 60 180 1800 15 60 10 5\n")
	b.WriteString("noforce\n")
	fmt.Fprintf(&b, "log %s D\n", c.LogPath)
	b.WriteString("rotate 7\n")
	b.WriteString(`logformat "L%d-%m-%Y %H:%M:%S %z %N.%p %E %U %C:%c %R:%r %O %I %h %T %e"` + "\n")
	b.WriteString("logdump 1048576 1048576\n")
	fmt.Fprintf(&b, "setgid %d\n", c.GID)
	fmt.Fprintf(&b, "setuid %d\n", c.UID)
	b.WriteString("maxconn 200\n")
	b.WriteString("auth strong\n")
	fmt.Fprintf(&b, "users %q\n", c.User+":CL:"+c.Password)
	fmt.Fprintf(&b, "external %s\n", c.ExternalIP)
	fmt.Fprintf(&b, "internal %s\n", c.InternalIP)
	b.WriteString("flush\n")
	fmt.Fprintf(&b, "allow %s\n", c.User)
	b.WriteString("deny *\n")
	fmt.Fprintf(&b, "proxy -p%d -a -4\n", c.Slot.HTTPPort())
	b.WriteString("flush\n")
	fmt.Fprintf(&b, "allow %s\n", c.User)
	b.WriteString("deny *\n")
	fmt.Fprintf(&b, "socks -p%d -a -4\n", c.Slot.SocksPort())
	return b.String()
}

type Proxy struct {
	cmd  *exec.Cmd
	Path string
}

func StartProxy(ctx context.Context, bin, cfgPath string) (*Proxy, error) {
	cmd := exec.Command(bin, cfgPath)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Proxy{cmd: cmd, Path: cfgPath}, nil
}

func (p *Proxy) Stop() {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return
	}
	p.cmd.Process.Kill()
	p.cmd.Wait()
}

func WaitListening(ctx context.Context, addr netip.Addr, port int, d time.Duration) error {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		ok, err := fw.HasListener(addr, port)
		if err == nil && ok {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("netns: no socket is listening on %s:%d after %s", addr, port, d)
}

type Responder struct {
	Tag      string
	StateDir string
}

func (r Responder) queryLog() string { return filepath.Join(r.StateDir, r.Tag+".dns") }

func (r Responder) Serve(web, dns netip.Addr) error {
	go r.serveDNS(dns)
	return r.serveHTTP(web)
}

func (r Responder) serveHTTP(bind netip.Addr) error {
	ln, err := net.Listen("tcp", net.JoinHostPort(bind.String(), "80"))
	if err != nil {
		return err
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go r.handleHTTP(conn)
	}
}

func (r Responder) handleHTTP(conn net.Conn) {
	defer conn.Close()
	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil {
		return
	}
	fields := strings.Fields(line)
	path := "/"
	if len(fields) >= 2 {
		path = fields[1]
	}
	for {
		h, err := br.ReadString('\n')
		if err != nil || strings.TrimSpace(h) == "" {
			break
		}
	}
	if strings.HasPrefix(path, "/slow") {
		time.Sleep(180 * time.Second)
	}
	remote := conn.RemoteAddr().String()
	host, _, _ := net.SplitHostPort(remote)
	body := fmt.Sprintf("%s src=%s path=%s\n", r.Tag, host, path)
	fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
}

func (r Responder) serveDNS(bind netip.Addr) {
	pc, err := net.ListenPacket("udp", net.JoinHostPort(bind.String(), "53"))
	if err != nil {
		return
	}
	buf := make([]byte, 2048)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		name := decodeQName(buf[:n])
		src, _, _ := net.SplitHostPort(addr.String())
		r.record(src, name)
		resp := buildAnswer(buf[:n], WebTarget)
		if resp != nil {
			pc.WriteTo(resp, addr)
		}
	}
}

func (r Responder) record(src, name string) {
	f, err := os.OpenFile(r.queryLog(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", src, name)
}

func decodeQName(msg []byte) string {
	if len(msg) < 13 {
		return ""
	}
	var parts []string
	i := 12
	for i < len(msg) && msg[i] != 0 {
		l := int(msg[i])
		if i+1+l > len(msg) {
			return ""
		}
		parts = append(parts, string(msg[i+1:i+1+l]))
		i += 1 + l
	}
	return strings.Join(parts, ".")
}

func buildAnswer(msg []byte, answer netip.Addr) []byte {
	if len(msg) < 13 {
		return nil
	}
	i := 12
	for i < len(msg) && msg[i] != 0 {
		i += 1 + int(msg[i])
	}
	qend := i + 5
	if qend > len(msg) {
		return nil
	}
	out := make([]byte, 0, qend+16)
	out = append(out, msg[0:2]...)
	out = append(out, 0x81, 0x80, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00)
	out = append(out, msg[12:qend]...)
	out = append(out, 0xc0, 0x0c, 0x00, 0x01, 0x00, 0x01)
	ttl := make([]byte, 4)
	binary.BigEndian.PutUint32(ttl, 5)
	out = append(out, ttl...)
	out = append(out, 0x00, 0x04)
	a := answer.As4()
	out = append(out, a[:]...)
	return out
}

func DNSQueries(stateDir, tag string) ([]string, error) {
	body, err := os.ReadFile(filepath.Join(stateDir, tag+".dns"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out, nil
}
