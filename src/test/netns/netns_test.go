//go:build netns

package netns

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/n4darae/huawei-API/src/internal/config"
	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/fw"
	"github.com/n4darae/huawei-API/src/internal/netcfg"
	"github.com/n4darae/huawei-API/src/internal/netcfg/files"
	"github.com/n4darae/huawei-API/src/internal/netcfg/linux"
)

var testSlots = []domain.Slot{domain.Slot(1), domain.Slot(2)}

const (
	decoyPort = 19999
	proxyUser = "cust_netnstest"
	proxyPass = "Kq7mZr2xTn9wLb4V"
)

func TestNetnsResponder(t *testing.T) {
	tag := os.Getenv(EnvResponder)
	if tag == "" {
		t.Skip("not a responder process")
	}
	r := Responder{Tag: tag, StateDir: os.Getenv(StateEnv)}
	if err := r.Serve(netip.MustParseAddr(os.Getenv("DONGLED_NETNS_WEB")), netip.MustParseAddr(os.Getenv("DONGLED_NETNS_DNS"))); err != nil {
		t.Fatalf("responder %s: %v", tag, err)
	}
}

func TestNetnsSuite(t *testing.T) {
	if os.Getenv(EnvResponder) != "" || os.Getenv(InnerEnv) != "" {
		t.Skip("child process")
	}
	if err := RequireRoot(); err != nil {
		t.Fatalf("%v (run: make test-netns)", err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("executable: %v", err)
	}
	bin, err := findProxyBinary()
	if err != nil {
		t.Fatalf("%v", err)
	}

	ctx := context.Background()
	before := snapshotRoot(ctx, t)

	stateDir, err := os.MkdirTemp("", "dongled-netns-")
	if err != nil {
		t.Fatalf("state dir: %v", err)
	}
	topo := NewTopology(testSlots, stateDir)
	t.Cleanup(func() {
		topo.Destroy(ctx)
		CleanupAll(ctx)
		os.RemoveAll(stateDir)
	})

	if err := topo.Build(ctx); err != nil {
		t.Fatalf("build topology: %v", err)
	}
	if err := topo.StartResponders(ctx, self); err != nil {
		t.Fatalf("start responders: %v", err)
	}

	cmd := exec.Command("ip", "netns", "exec", HostNS, self,
		"-test.run", "TestNetnsInner", "-test.v", "-test.timeout", "8m")
	cmd.Env = append(os.Environ(),
		InnerEnv+"=1",
		StateEnv+"="+stateDir,
		"DONGLED_3PROXY_BIN="+bin,
	)
	out, runErr := cmd.CombinedOutput()
	t.Logf("inner suite output:\n%s", out)
	if runErr != nil {
		t.Fatalf("inner suite failed: %v", runErr)
	}

	topo.Destroy(ctx)
	assertRootUntouched(ctx, t, before)
}

func findProxyBinary() (string, error) {
	candidates := []string{os.Getenv("DONGLED_3PROXY_BIN"), config.Bin3proxy, "third_party/3proxy/bin/3proxy"}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return filepath.Abs(c)
		}
	}
	return "", fmt.Errorf("netns: no 3proxy binary; set DONGLED_3PROXY_BIN or install one at %s", config.Bin3proxy)
}

type rootState struct {
	rules    string
	tables   string
	links    string
	services map[string]string
	jails    string
	ufw      string
}

func snapshotRoot(ctx context.Context, t *testing.T) rootState {
	t.Helper()
	s := rootState{services: map[string]string{}}
	s.rules = capture(ctx, "ip", "rule", "show")
	s.tables = capture(ctx, "nft", "list", "tables")
	s.links = capture(ctx, "ip", "-br", "link")
	s.jails = capture(ctx, "fail2ban-client", "status")
	s.ufw = firstLine(capture(ctx, "ufw", "status"))
	for _, unit := range []string{"nginx", "mysql", "ufw", "fail2ban", "tailscaled"} {
		s.services[unit] = strings.TrimSpace(capture(ctx, "systemctl", "is-active", unit))
	}
	return s
}

func capture(ctx context.Context, args ...string) string {
	out, _ := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
	return string(out)
}

func firstLine(s string) string {
	if i := strings.Index(s, "\n"); i >= 0 {
		return s[:i]
	}
	return s
}

func assertRootUntouched(ctx context.Context, t *testing.T, before rootState) {
	t.Helper()
	after := snapshotRoot(ctx, t)
	if before.rules != after.rules {
		t.Errorf("root netns ip rules changed\nbefore:\n%s\nafter:\n%s", before.rules, after.rules)
	}
	if before.tables != after.tables {
		t.Errorf("root netns nft tables changed\nbefore:\n%s\nafter:\n%s", before.tables, after.tables)
	}
	if before.links != after.links {
		t.Errorf("root netns links changed\nbefore:\n%s\nafter:\n%s", before.links, after.links)
	}
	if !strings.Contains(after.ufw, "active") {
		t.Errorf("ufw is no longer active: %q", after.ufw)
	}
	for _, jail := range []string{"sshd", "webterm-ban", "webterm-lockout"} {
		if !strings.Contains(after.jails, jail) {
			t.Errorf("fail2ban jail %s disappeared:\n%s", jail, after.jails)
		}
	}
	for unit, state := range before.services {
		if after.services[unit] != state {
			t.Errorf("service %s went from %q to %q", unit, state, after.services[unit])
		}
	}
	leftovers := capture(ctx, "ip", "netns", "list")
	for _, line := range strings.Split(leftovers, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), Prefix) {
			t.Errorf("test namespace left behind: %s", line)
		}
	}
	t.Logf("root netns ip rules unchanged:\n%s", after.rules)
	t.Logf("root netns nft tables unchanged:\n%s", after.tables)
	t.Logf("services: %v", after.services)
}

type harness struct {
	t        *testing.T
	ctx      context.Context
	stateDir string
	cfgDir   string
	netDir   string
	firewall *fw.Nft
	live     *linux.Manager
	applied  *linux.Manager
	verbs    *verbRecorder
	proxies  map[domain.Slot]*Proxy
}

type verbRecorder struct{ calls []string }

func (v *verbRecorder) exec(ctx context.Context, name string, args ...string) ([]byte, error) {
	full := strings.Join(append([]string{name}, args...), " ")
	v.calls = append(v.calls, full)
	if name == "udevadm" || name == "networkctl" {
		return nil, nil
	}
	return netcfg.SystemExec(ctx, name, args...)
}

func (v *verbRecorder) has(sub string) bool {
	for _, c := range v.calls {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}

func TestNetnsInner(t *testing.T) {
	if os.Getenv(InnerEnv) == "" {
		t.Skip("outer process")
	}
	if err := RequireRoot(); err != nil {
		t.Fatalf("%v", err)
	}
	if err := RefuseRootNetns(); err != nil {
		t.Fatalf("%v", err)
	}

	ctx := context.Background()
	h := &harness{
		t:        t,
		ctx:      ctx,
		stateDir: os.Getenv(StateEnv),
		cfgDir:   t.TempDir(),
		netDir:   t.TempDir(),
		proxies:  map[domain.Slot]*Proxy{},
	}
	os.Chmod(h.cfgDir, 0o777)

	h.verbs = &verbRecorder{}
	h.applied = linux.New(linux.Options{
		NetworkDir:   h.netDir,
		RtTablesFile: filepath.Join(h.netDir, "dongled.conf"),
		Exec:         h.verbs.exec,
		Slots:        testSlots,
	})
	h.live = linux.New(linux.Options{
		NetworkDir:   h.netDir,
		RtTablesFile: filepath.Join(h.netDir, "dongled.conf"),
		Exec:         netcfg.SystemExec,
		ProbeDst:     WebTarget,
		Slots:        testSlots,
	})
	h.firewall = fw.NewNft(fw.Options{Exec: fw.SystemExec})

	t.Run("routing", h.setupRouting)
	t.Run("firewall", h.setupFirewall)
	t.Run("proxies", h.startProxies)

	t.Run("A1_egress_leaves_via_that_dongle", h.assertEgressPerDongle)
	t.Run("A2_dns_query_source_is_the_dongle", h.assertDNSContained)
	t.Run("A3_customer_leg_handshake_completes", h.assertCustomerLeg)
	t.Run("A4_sock_destroy_kills_a_hung_connection", h.assertSockDestroy)
	t.Run("A6_rule_counters_read_with_reset_rules", h.assertCounters)
	t.Run("A7_farm_local_probe_completes", h.assertLocalProbeCompletes)
	t.Run("A8_loopback_rule_is_not_an_escape_hatch", h.assertLoopbackIsNotAnEscapeHatch)

	t.Run("invariants", h.assertInvariants)
	t.Run("negative_controls", h.assertNegativeControls)
	t.Run("link_subscription", h.assertSubscribe)
	t.Run("remove_slot_cleans_up", h.assertRemoveSlot)

	t.Cleanup(h.stopProxies)
}

func (h *harness) setupRouting(t *testing.T) {
	if err := h.applied.EnsureRouteTableNames(h.ctx); err != nil {
		t.Fatalf("EnsureRouteTableNames: %v", err)
	}
	if err := h.live.EnsureGlobal(h.ctx, []netip.Addr{PublicIP}); err != nil {
		t.Fatalf("EnsureGlobal: %v", err)
	}
	if err := h.applied.EnsureGlobal(h.ctx, []netip.Addr{PublicIP}); err != nil {
		t.Fatalf("EnsureGlobal on the file manager: %v", err)
	}

	for _, s := range testSlots {
		if err := h.applied.ApplySlot(h.ctx, s, "pci-0000:00:14.0-usb-0:13."+s.String()+":1.0", ""); err != nil {
			t.Fatalf("ApplySlot %s: %v", s, err)
		}
		body, err := os.ReadFile(filepath.Join(h.netDir, s.NetworkFileName()))
		if err != nil {
			t.Fatalf("read rendered network file: %v", err)
		}
		if err := ApplyNetworkFile(h.ctx, "", body); err != nil {
			t.Fatalf("apply rendered network file for %s: %v", s, err)
		}
	}

	if !h.verbs.has("udevadm control --reload") || !h.verbs.has("udevadm trigger --subsystem-match=net --action=add") {
		t.Fatalf("the link change must use the udev verbs, got %v", h.verbs.calls)
	}
	if !h.verbs.has("networkctl reload") || !h.verbs.has("networkctl reconfigure dg01") {
		t.Fatalf("the network change must use the networkctl verbs, got %v", h.verbs.calls)
	}
	for _, c := range h.verbs.calls {
		if strings.Contains(c, "netplan") {
			t.Fatalf("netplan bounces the uplink: %q", c)
		}
	}

	prios, err := RulePriorities(h.ctx, "")
	if err != nil {
		t.Fatalf("rule priorities: %v", err)
	}
	t.Logf("ip rule priorities in the host namespace: %v", prios)
	want := []int{0, domain.RulePrioPublic}
	for _, s := range testSlots {
		want = append(want, s.RulePrioSrc(), s.RulePrioUID())
	}
	for _, w := range want {
		found := false
		for _, p := range prios {
			if p == w {
				found = true
			}
		}
		if !found {
			t.Fatalf("rule priority %d is missing from %v", w, prios)
		}
	}
	for _, s := range testSlots {
		if err := assertOrdered(prios, domain.RulePrioPublic, s.RulePrioSrc(), s.RulePrioUID()); err != nil {
			t.Fatalf("slot %s: %v", s, err)
		}
	}
}

func assertOrdered(prios []int, want ...int) error {
	idx := map[int]int{}
	for i, p := range prios {
		if _, seen := idx[p]; !seen {
			idx[p] = i
		}
	}
	for i := 1; i < len(want); i++ {
		if idx[want[i-1]] >= idx[want[i]] {
			return fmt.Errorf("priority %d must be evaluated before %d", want[i-1], want[i])
		}
	}
	return nil
}

func (h *harness) setupFirewall(t *testing.T) {
	if err := RefuseRootNetns(); err != nil {
		t.Fatalf("%v", err)
	}
	if err := h.firewall.EnsureTable(h.ctx); err != nil {
		t.Fatalf("EnsureTable: %v", err)
	}
	if err := h.firewall.Verify(h.ctx); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if err := h.firewall.AddPublic(h.ctx, PublicIf, PublicIP); err != nil {
		t.Fatalf("AddPublic: %v", err)
	}
	for _, s := range testSlots {
		if err := h.firewall.AddDongle(h.ctx, s.IfaceName(), s.GatewayIP()); err != nil {
			t.Fatalf("AddDongle %s: %v", s, err)
		}
	}
	members, err := h.firewall.SetMembers(h.ctx, fw.SetDongleIfaces)
	if err != nil {
		t.Fatalf("SetMembers: %v", err)
	}
	t.Logf("dongle_ifaces = %v", members)
	if len(members) != len(testSlots) {
		t.Fatalf("want %d dongle interfaces, got %v", len(testSlots), members)
	}
}

func (h *harness) startProxies(t *testing.T) {
	bin := os.Getenv("DONGLED_3PROXY_BIN")
	for _, s := range testSlots {
		logPath := filepath.Join(h.cfgDir, s.UserName()+".log")
		cfg := ProxyConfig{
			Slot:       s,
			InternalIP: PublicIP,
			ExternalIP: s.HostIP(),
			NServer:    DNSServer,
			User:       proxyUser,
			Password:   proxyPass,
			LogPath:    logPath,
			UID:        s.UID(),
			GID:        config.GroupGID,
		}
		path := filepath.Join(h.cfgDir, s.UserName()+".cfg")
		if err := os.WriteFile(path, []byte(cfg.Render()), 0o644); err != nil {
			t.Fatalf("write config: %v", err)
		}
		p, err := StartProxy(h.ctx, bin, path)
		if err != nil {
			t.Fatalf("start 3proxy for %s: %v", s, err)
		}
		h.proxies[s] = p
		if err := WaitListening(h.ctx, PublicIP, s.SocksPort(), 15*time.Second); err != nil {
			t.Fatalf("3proxy for %s never bound a listener (it exits 0 with no listener, so an exit code proves nothing): %v", s, err)
		}
		t.Logf("slot %s listening on %s:%d as uid %d gid %d", s, PublicIP, s.SocksPort(), s.UID(), config.GroupGID)
	}
}

func (h *harness) stopProxies() {
	for _, p := range h.proxies {
		p.Stop()
	}
}

func (h *harness) socksFetch(t *testing.T, s domain.Slot, url string, hostname bool) string {
	t.Helper()
	flag := "--socks5"
	if hostname {
		flag = "--socks5-hostname"
	}
	out, err := RunIn(h.ctx, CustNS, "curl", "-sS", "--max-time", "8", flag,
		fmt.Sprintf("%s:%s@%s:%d", proxyUser, proxyPass, PublicIP, s.SocksPort()), url)
	if err != nil {
		t.Fatalf("socks fetch via slot %s: %v", s, err)
	}
	return strings.TrimSpace(string(out))
}

func (h *harness) assertEgressPerDongle(t *testing.T) {
	loopBefore, err := h.firewall.RuleHits(h.ctx, fw.CommentLoopbackLeg)
	if err != nil {
		t.Fatalf("RuleHits: %v", err)
	}
	for _, s := range testSlots {
		body := h.socksFetch(t, s, "http://"+WebTarget.String()+"/egress", false)
		t.Logf("slot %s -> %q", s, body)
		wantTag := fmt.Sprintf("dongle-%02d", int(s))
		if !strings.HasPrefix(body, wantTag+" ") {
			t.Fatalf("slot %s egressed to %q, want the responder inside %s", s, body, DongleNS(s))
		}
		if !strings.Contains(body, "src="+s.HostIP().String()) {
			t.Fatalf("slot %s arrived with the wrong source address: %q", s, body)
		}
		if strings.Contains(body, "public-leak") {
			t.Fatalf("slot %s egressed through the public uplink: %q", s, body)
		}
	}
	loopAfter, err := h.firewall.RuleHits(h.ctx, fw.CommentLoopbackLeg)
	if err != nil {
		t.Fatalf("RuleHits: %v", err)
	}
	if loopAfter != loopBefore {
		t.Fatalf("real customer traffic matched the loopback exception %d times; it must only ever carry farm-local probes", loopAfter-loopBefore)
	}
	t.Logf("customer traffic did not touch the farm-local probe rule (%d matches)", loopBefore)
}

func (h *harness) assertDNSContained(t *testing.T) {
	for _, s := range testSlots {
		host := fmt.Sprintf("probe-%d-%d.test", int(s), time.Now().UnixNano())
		body := h.socksFetch(t, s, "http://"+host+"/dns", true)
		t.Logf("slot %s resolved %s -> %q", s, host, body)

		dongleTag := fmt.Sprintf("dongle-%02d", int(s))
		queries, err := DNSQueries(h.stateDir, dongleTag)
		if err != nil {
			t.Fatalf("read dns log: %v", err)
		}
		want := s.HostIP().String() + " " + host
		found := false
		for _, q := range queries {
			if q == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("the resolver binds 0.0.0.0, so only the uidrange rule contains it; want %q in %s queries %v", want, dongleTag, queries)
		}

		leaked, err := DNSQueries(h.stateDir, "public-leak")
		if err != nil {
			t.Fatalf("read leak dns log: %v", err)
		}
		for _, q := range leaked {
			if strings.HasSuffix(q, " "+host) {
				t.Fatalf("dns query for %s leaked out of the public leg: %q", host, q)
			}
		}
	}
}

func (h *harness) assertCustomerLeg(t *testing.T) {
	for _, s := range testSlots {
		addr := fmt.Sprintf("%s/%d", PublicIP, s.SocksPort())
		_, err := RunIn(h.ctx, CustNS, "timeout", "5", "bash", "-c",
			"exec 3<>/dev/tcp/"+strings.ReplaceAll(addr, "/", "/")+" && echo ok")
		if err != nil {
			t.Fatalf("the customer tcp handshake to slot %s never completed (uidrange hijacks the SYN-ACK and meta skgid matches it in the output hook): %v", s, err)
		}
		t.Logf("slot %s customer handshake completed", s)
	}
	hits, err := h.firewall.CustomerAcceptHits(h.ctx)
	if err != nil {
		t.Fatalf("CustomerAcceptHits: %v", err)
	}
	if hits == 0 {
		t.Fatal("the customer-facing accept rule matched zero packets, so something else let the handshake through")
	}
	t.Logf("customer-facing accept rule matched %d packets", hits)
}

func (h *harness) assertSockDestroy(t *testing.T) {
	slot := testSlots[0]
	src := slot.HostIP()
	dongleNS := DongleNS(slot)

	go func() {
		RunIn(context.Background(), CustNS, "curl", "-sS", "--max-time", "120", "--socks5",
			fmt.Sprintf("%s:%s@%s:%d", proxyUser, proxyPass, PublicIP, slot.SocksPort()),
			"http://"+WebTarget.String()+"/slow")
	}()

	if err := waitFor(20*time.Second, func() bool {
		n, err := fw.CountEstablishedFrom(src)
		return err == nil && n > 0
	}); err != nil {
		t.Fatalf("no established socket from %s appeared: %v", src, err)
	}

	if _, err := RunIn(h.ctx, dongleNS, "nft", "add", "table", "inet", "blackhole"); err != nil {
		t.Fatalf("blackhole table: %v", err)
	}
	if _, err := RunIn(h.ctx, dongleNS, "nft", "add", "chain", "inet", "blackhole", "input",
		"{ type filter hook input priority 0 ; policy accept ; }"); err != nil {
		t.Fatalf("blackhole chain: %v", err)
	}
	if _, err := RunIn(h.ctx, dongleNS, "nft", "add", "rule", "inet", "blackhole", "input",
		"ip", "saddr", src.String(), "drop"); err != nil {
		t.Fatalf("blackhole rule: %v", err)
	}
	time.Sleep(2 * time.Second)

	stillUp, err := fw.CountEstablishedFrom(src)
	if err != nil {
		t.Fatalf("CountEstablishedFrom: %v", err)
	}
	if stillUp == 0 {
		t.Fatal("a blackholed connection should hang in ESTABLISHED, not disappear")
	}
	t.Logf("%d connection(s) from %s are hung against a blackhole", stillUp, src)

	deleted, err := h.firewall.FlushConntrack(h.ctx, src)
	if err != nil {
		t.Fatalf("FlushConntrack: %v", err)
	}
	t.Logf("conntrack entries deleted: %d", deleted)
	if deleted == 0 {
		t.Fatal("conntrack must report a real count, not a silent zero")
	}

	killed, err := h.firewall.KillSockets(h.ctx, src)
	if err != nil {
		t.Fatalf("KillSockets: %v", err)
	}
	t.Logf("conns_killed = %d", killed)
	if killed == 0 {
		t.Fatal("SOCK_DESTROY matched zero sockets; a fence that kills nothing looks identical to one that works")
	}
	if err := waitFor(10*time.Second, func() bool {
		n, err := fw.CountEstablishedFrom(src)
		return err == nil && n == 0
	}); err != nil {
		t.Fatalf("the hung socket survived SOCK_DESTROY: %v", err)
	}

	RunIn(h.ctx, dongleNS, "nft", "delete", "table", "inet", "blackhole")
}

func waitFor(d time.Duration, fn func() bool) error {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if fn() {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("condition not met within %s", d)
}

func (h *harness) assertCounters(t *testing.T) {
	if err := h.firewall.ResetRules(h.ctx); err != nil {
		t.Fatalf("nft reset rules: %v", err)
	}
	after, err := h.firewall.CustomerAcceptHits(h.ctx)
	if err != nil {
		t.Fatalf("CustomerAcceptHits: %v", err)
	}
	if after != 0 {
		t.Fatalf("reset rules must zero the rule counter, got %d", after)
	}
	slot := testSlots[0]
	h.socksFetch(t, slot, "http://"+WebTarget.String()+"/counter", false)
	hits, err := h.firewall.CustomerAcceptHits(h.ctx)
	if err != nil {
		t.Fatalf("CustomerAcceptHits: %v", err)
	}
	if hits == 0 {
		t.Fatal("the customer accept rule did not count the new request")
	}
	t.Logf("after reset rules, one customer request produced %d matches on the customer-facing accept", hits)
}

func (h *harness) assertInvariants(t *testing.T) {
	if v := h.live.AssertInvariants(h.ctx); len(v) != 0 {
		for _, x := range v {
			t.Errorf("invariant %s violated: %s", x.Name, x.Detail)
		}
		t.FailNow()
	}
	t.Log("all routing invariants hold")

	if _, err := Run(h.ctx, "sysctl", "-qw", "net.ipv4.conf.all.rp_filter=1"); err != nil {
		t.Fatalf("sysctl: %v", err)
	}
	v := h.live.AssertInvariants(h.ctx)
	found := false
	for _, x := range v {
		if x.Name == domain.InvariantRpFilterAll {
			found = true
		}
	}
	if _, err := Run(h.ctx, "sysctl", "-qw", "net.ipv4.conf.all.rp_filter=2"); err != nil {
		t.Fatalf("sysctl restore: %v", err)
	}
	if !found {
		t.Fatal("AssertInvariants did not notice rp_filter changing, so it is not reading this namespace")
	}

	obs, err := h.live.Observe(h.ctx)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if len(obs.DuplicateAddrs) != 0 {
		t.Fatalf("duplicate addresses: %v", obs.DuplicateAddrs)
	}
	if len(obs.ForeignRuleBelowCeil) != 0 {
		t.Fatalf("foreign rules below the ceiling: %v", obs.ForeignRuleBelowCeil)
	}
	if len(obs.PublicSrcRules) != 1 {
		t.Fatalf("want exactly one public source rule, got %v", obs.PublicSrcRules)
	}
	for _, s := range testSlots {
		l, ok := obs.Links[s.IfaceName()]
		if !ok {
			t.Fatalf("link %s missing from the observation", s.IfaceName())
		}
		if !l.Up() {
			t.Fatalf("link %s operstate %q must count as up", s.IfaceName(), l.OperState)
		}
	}
	if !obs.RouteTableNamesOK {
		t.Fatal("route table names were not written")
	}
}

func (h *harness) assertNegativeControls(t *testing.T) {
	slot := testSlots[0]
	out, err := Run(h.ctx, "ip", "route", "get", CustIP.String(), "from", PublicIP.String(), "uid", fmt.Sprint(slot.UID()))
	if err != nil {
		t.Fatalf("route get: %v", err)
	}
	if !strings.Contains(string(out), "dev "+PublicIf) {
		t.Fatalf("with the priority %d rule the customer reply must leave via %s: %s", domain.RulePrioPublic, PublicIf, out)
	}

	if _, err := Run(h.ctx, "ip", "rule", "del", "from", PublicIP.String()+"/32", "iif", "lo", "lookup", "main", "priority", fmt.Sprint(domain.RulePrioPublic)); err != nil {
		t.Fatalf("remove the public rule: %v", err)
	}
	out, err = Run(h.ctx, "ip", "route", "get", CustIP.String(), "from", PublicIP.String(), "uid", fmt.Sprint(slot.UID()))
	if err != nil {
		t.Fatalf("route get: %v", err)
	}
	t.Logf("without the priority %d rule the customer reply is routed: %s", domain.RulePrioPublic, strings.TrimSpace(string(out)))
	if !strings.Contains(string(out), "dev "+slot.IfaceName()) {
		t.Fatalf("the measured bug is that uidrange hijacks the reply onto %s; the harness no longer reproduces it: %s", slot.IfaceName(), out)
	}
	if err := h.live.EnsureGlobal(h.ctx, []netip.Addr{PublicIP}); err != nil {
		t.Fatalf("restore the public rule: %v", err)
	}

	if v := h.live.AssertInvariants(h.ctx); len(v) != 0 {
		t.Fatalf("invariants must hold again after restoring the rule: %v", v)
	}

	body, err := files.RenderNetwork(slot)
	if err != nil {
		t.Fatalf("RenderNetwork: %v", err)
	}
	if strings.Contains(string(body), "OutgoingInterface") {
		t.Fatal("the oif rule was measured to match zero packets and must stay deleted")
	}
}

func (h *harness) assertSubscribe(t *testing.T) {
	ctx, cancel := context.WithCancel(h.ctx)
	defer cancel()
	events, stop, err := h.live.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer stop()

	if _, err := Run(h.ctx, "ip", "link", "add", "dgltmp0", "type", "dummy"); err != nil {
		t.Fatalf("create dummy link: %v", err)
	}
	defer Run(h.ctx, "ip", "link", "del", "dgltmp0")

	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("the subscription closed before the link event arrived")
			}
			if ev.Link.Name == "dgltmp0" {
				t.Logf("link event: kind=%s name=%s operstate=%s", ev.Kind, ev.Link.Name, ev.Link.OperState)
				return
			}
		case <-deadline:
			t.Fatal("no rtnetlink link event arrived within 10s")
		}
	}
}

func (h *harness) assertRemoveSlot(t *testing.T) {
	slot := testSlots[1]
	h.proxies[slot].Stop()
	delete(h.proxies, slot)

	if err := h.applied.RemoveSlot(h.ctx, slot); err != nil {
		t.Fatalf("RemoveSlot: %v", err)
	}
	for _, name := range []string{slot.LinkFileName(), slot.NetworkFileName()} {
		if _, err := os.Stat(filepath.Join(h.netDir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s should be gone, got %v", name, err)
		}
	}
	prios, err := RulePriorities(h.ctx, "")
	if err != nil {
		t.Fatalf("rule priorities: %v", err)
	}
	for _, gone := range []int{slot.RulePrioSrc(), slot.RulePrioUID()} {
		for _, p := range prios {
			if p == gone {
				t.Fatalf("rule priority %d survived RemoveSlot: %v", gone, prios)
			}
		}
	}
	t.Logf("after removing slot %s the rules are %v", slot, prios)

	if err := h.applied.RemoveSlot(h.ctx, slot); err != nil {
		t.Fatalf("a second RemoveSlot must be a no-op, got %v", err)
	}
	if err := h.firewall.RemoveDongle(h.ctx, slot.IfaceName()); err != nil {
		t.Fatalf("RemoveDongle: %v", err)
	}
	members, err := h.firewall.SetMembers(h.ctx, fw.SetDongleIfaces)
	if err != nil {
		t.Fatalf("SetMembers: %v", err)
	}
	for _, m := range members {
		if m == slot.IfaceName() {
			t.Fatalf("%s is still in dongle_ifaces: %v", slot.IfaceName(), members)
		}
	}
	if err := h.firewall.RemoveDongle(h.ctx, slot.IfaceName()); err != nil {
		t.Fatalf("a second RemoveDongle must be a no-op, got %v", err)
	}
}

func (h *harness) assertLocalProbeCompletes(t *testing.T) {
	slot := testSlots[0]
	before, err := h.firewall.RuleHits(h.ctx, fw.CommentLoopbackLeg)
	if err != nil {
		t.Fatalf("RuleHits: %v", err)
	}

	out, err := RunIn(h.ctx, "", "curl", "-sS", "--max-time", "8", "--socks5",
		fmt.Sprintf("%s:%s@%s:%d", proxyUser, proxyPass, PublicIP, slot.SocksPort()),
		"http://"+WebTarget.String()+"/local-probe")
	if err != nil {
		t.Fatalf("an authenticated probe run on the farm host itself must complete: %v", err)
	}
	body := strings.TrimSpace(string(out))
	t.Logf("farm-local authenticated probe through slot %s -> %q", slot, body)
	if !strings.HasPrefix(body, "dongle-01 ") {
		t.Fatalf("the local probe did not reach the dongle responder: %q", body)
	}

	after, err := h.firewall.RuleHits(h.ctx, fw.CommentLoopbackLeg)
	if err != nil {
		t.Fatalf("RuleHits: %v", err)
	}
	if after <= before {
		t.Fatalf("the farm-local probe rule counted %d packets before and %d after; something else carried the reply", before, after)
	}
	t.Logf("farm-local probe leg matched %d packets", after-before)
}

func (h *harness) assertLoopbackIsNotAnEscapeHatch(t *testing.T) {
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", LocalDecoy, decoyPort))
	if err != nil {
		t.Fatalf("decoy listener: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	loopBefore, err := h.firewall.RuleHits(h.ctx, fw.CommentLoopbackLeg)
	if err != nil {
		t.Fatalf("RuleHits: %v", err)
	}
	leakBefore, err := h.leakHits()
	if err != nil {
		t.Fatalf("leak hits: %v", err)
	}

	_, err = Run(h.ctx, "setpriv", "--regid", fmt.Sprint(config.GroupGID), "--clear-groups",
		"curl", "-sS", "--max-time", "4", "--interface", PublicIP.String(),
		fmt.Sprintf("http://%s:%d/", LocalDecoy, decoyPort))
	if err == nil {
		t.Fatal("gid 6100 opened a new outbound connection over lo; the loopback exception is an escape hatch")
	}
	t.Logf("gid %d could not open a new connection over lo to %s:%d (%v)", config.GroupGID, LocalDecoy, decoyPort, err)

	loopAfter, err := h.firewall.RuleHits(h.ctx, fw.CommentLoopbackLeg)
	if err != nil {
		t.Fatalf("RuleHits: %v", err)
	}
	if loopAfter != loopBefore {
		t.Fatalf("the farm-local probe rule matched %d new packets; it must only ever match replies from a proxy port", loopAfter-loopBefore)
	}
	leakAfter, err := h.leakHits()
	if err != nil {
		t.Fatalf("leak hits: %v", err)
	}
	if leakAfter <= leakBefore {
		t.Fatalf("the attempt never reached the leak drop (%d -> %d), so this test proved nothing", leakBefore, leakAfter)
	}
	t.Logf("the attempt was dropped by the leak rules (%d -> %d matches)", leakBefore, leakAfter)
}

func (h *harness) leakHits() (uint64, error) {
	var total uint64
	for _, comment := range []string{"leak log", "leak drop"} {
		n, err := h.firewall.RuleHits(h.ctx, comment)
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}
