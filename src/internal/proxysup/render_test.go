package proxysup

import (
	"bytes"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n4darae/huawei-API/src/internal/config"
	"github.com/n4darae/huawei-API/src/internal/domain"
)

func testSpec(t *testing.T) Spec {
	t.Helper()
	sp := NewSpec(1, netip.MustParseAddr("139.99.68.39"), netip.MustParseAddr("1.1.1.1"))
	sp.Users = []User{{Name: "cust_ab12cd34", Password: "Kq7mZr2xTn9wLb4V"}}
	sp.LogPath = "/var/log/dongled/px01.log"
	sp.ConfigPath = "/etc/dongled/proxy/px01.cfg"
	return sp
}

func mustRender(t *testing.T, sp Spec) []byte {
	t.Helper()
	out, err := Render(sp)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return out
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v (rerun with UPDATE_GOLDEN=1)", err)
	}
	if !bytes.Equal(want, got) {
		t.Fatalf("golden %s mismatch\n--- want ---\n%s\n--- got ---\n%s", name, want, got)
	}
}

func TestRenderGoldenUserPass(t *testing.T) {
	golden(t, "px01_userpass.cfg", mustRender(t, testSpec(t)))
}

func TestRenderGoldenIPList(t *testing.T) {
	sp := testSpec(t)
	sp.AuthMode = domain.AuthIPList
	sp.Users = nil
	sp.AuthIPs = []netip.Prefix{
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.5/32"),
	}
	golden(t, "px01_iplist.cfg", mustRender(t, sp))
}

func TestRenderGoldenBoth(t *testing.T) {
	sp := testSpec(t)
	sp.AuthMode = domain.AuthBoth
	sp.AuthIPs = []netip.Prefix{netip.MustParsePrefix("203.0.113.5/32")}
	golden(t, "px01_both.cfg", mustRender(t, sp))
}

func TestRenderGoldenNarrowedPorts(t *testing.T) {
	sp := testSpec(t)
	sp.Policy.AllowAllPorts = false
	sp.Policy.AllowedPorts = []domain.PortRange{{Lo: 80, Hi: 80}, {Lo: 443, Hi: 443}, {Lo: 8000, Hi: 8100}}
	golden(t, "px01_narrow_ports.cfg", mustRender(t, sp))
}

func TestRenderDefaultsToOpenPorts(t *testing.T) {
	out := string(mustRender(t, testSpec(t)))
	if !strings.Contains(out, "\nallow cust_ab12cd34\n") {
		t.Fatalf("open port policy must render a bare allow line:\n%s", out)
	}
	for _, p := range domain.SMTPPorts {
		if strings.Contains(out, string(rune(p))) && strings.Contains(out, "deny") && strings.Contains(out, "25,465,587") {
			t.Fatalf("smtp blocking belongs in nft, not the acl:\n%s", out)
		}
	}
}

func TestRenderCarriesRequiredGlobals(t *testing.T) {
	out := string(mustRender(t, testSpec(t)))
	for _, want := range []string{
		"noforce\n",
		"auth strong\n",
		"timeouts " + Timeouts + "\n",
		"logformat " + LogFormat + "\n",
		"internal 139.99.68.39\n",
		"external 192.168.101.100\n",
		"nserver 192.168.101.1\n",
		"nserver 1.1.1.1\n",
		"proxy -p22001 -a -4\n",
		"socks -p21001 -a -4\n",
		"deny *\n",
		"flush\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "daemon") {
		t.Fatalf("config must not daemonize under systemd:\n%s", out)
	}
	if strings.Contains(out, "-a1") || strings.Contains(out, "-a2") {
		t.Fatalf("anonymous flag must be exactly -a:\n%s", out)
	}
}

func TestRenderTimeoutsHasTenValues(t *testing.T) {
	for _, l := range Parse(mustRender(t, testSpec(t))) {
		if l.Cmd() != "timeouts" {
			continue
		}
		if got := len(l.Args()); got != TimeoutsCount {
			t.Fatalf("timeouts carries %d values, want %d: %q", got, TimeoutsCount, l.Raw)
		}
		return
	}
	t.Fatal("no timeouts line")
}

func TestRenderQuotesUsersLine(t *testing.T) {
	out := mustRender(t, testSpec(t))
	if !bytes.Contains(out, []byte(`users "cust_ab12cd34:CL:Kq7mZr2xTn9wLb4V"`)) {
		t.Fatalf("users line must be fully quoted:\n%s", out)
	}
}

func TestRenderSetuidAndUnitUserComeFromTheSameSlot(t *testing.T) {
	for _, slot := range []domain.Slot{1, 7, 48} {
		sp := NewSpec(slot, netip.MustParseAddr("139.99.68.39"), netip.MustParseAddr("1.1.1.1"))
		sp.Users = []User{{Name: "cust", Password: "Kq7mZr2xTn9wLb4V"}}
		out := mustRender(t, sp)

		var uid, gid string
		for _, l := range Parse(out) {
			switch l.Cmd() {
			case "setuid":
				uid = l.Arg(0)
			case "setgid":
				gid = l.Arg(0)
			}
		}
		unitSlot, ok := SlotFromUnit(sp.Unit())
		if !ok {
			t.Fatalf("unit %q does not resolve to a slot", sp.Unit())
		}
		if unitSlot != slot {
			t.Fatalf("unit %q resolves to slot %d, want %d", sp.Unit(), unitSlot, slot)
		}
		if want := itoa(unitSlot.UID()); uid != want {
			t.Fatalf("slot %d: setuid %q, unit user %s implies %s", slot, uid, unitSlot.UserName(), want)
		}
		if want := itoa(unitSlot.GID()); gid != want {
			t.Fatalf("slot %d: setgid %q, want %s", slot, gid, want)
		}
	}

	unit := string(RenderUnit(UnitOptions{}))
	if !strings.Contains(unit, "User=%i\n") {
		t.Fatalf("unit must derive User from the instance name:\n%s", unit)
	}
	if !strings.Contains(unit, "Group="+config.GroupName+"\n") {
		t.Fatalf("unit must set Group=%s:\n%s", config.GroupName, unit)
	}
	if strings.Contains(unit, "PartOf=") {
		t.Fatalf("PartOf would stop every proxy with the panel:\n%s", unit)
	}
	if !strings.Contains(unit, "ExecReload=/bin/kill -USR1 $MAINPID\n") {
		t.Fatalf("unit must reload with SIGUSR1:\n%s", unit)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestRenderRefusals(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Spec)
		want error
	}{
		{"empty user list", func(sp *Spec) { sp.Users = nil }, ErrNoUsers},
		{"iplist without addresses", func(sp *Spec) {
			sp.AuthMode = domain.AuthIPList
			sp.Users = nil
		}, ErrNoAuthIPs},
		{"both without addresses", func(sp *Spec) { sp.AuthMode = domain.AuthBoth }, ErrNoAuthIPs},
		{"iplist keeps users", func(sp *Spec) {
			sp.AuthMode = domain.AuthIPList
			sp.AuthIPs = []netip.Prefix{netip.MustParsePrefix("203.0.113.5/32")}
		}, ErrUsersUnused},
		{"uid not from slot", func(sp *Spec) { sp.UID = 4242 }, ErrUIDMismatch},
		{"gid not from slot", func(sp *Spec) { sp.GID = 4242 }, ErrUIDMismatch},
		{"port not from slot", func(sp *Spec) { sp.SocksPort = 21999 }, ErrPortMismatch},
		{"password with a dollar sign", func(sp *Spec) {
			sp.Users[0].Password = "Kq7mZr2xTn9wLb4$"
		}, ErrBadPassword},
		{"password too short", func(sp *Spec) { sp.Users[0].Password = "short" }, ErrBadPassword},
		{"username with a colon", func(sp *Spec) { sp.Users[0].Name = "cust:1" }, ErrBadUsername},
		{"no nserver", func(sp *Spec) { sp.NServers = nil }, ErrNoNServer},
		{"internal is not v4", func(sp *Spec) { sp.InternalIP = netip.Addr{} }, ErrBadAddr},
		{"port policy denies everything", func(sp *Spec) {
			sp.Policy.AllowAllPorts = false
			sp.Policy.AllowedPorts = nil
		}, domain.ErrInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sp := testSpec(t)
			sp.Users = append([]User(nil), sp.Users...)
			tc.mut(&sp)
			_, err := Render(sp)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Render error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestVerifyRefusals(t *testing.T) {
	sp := testSpec(t)
	base := string(mustRender(t, sp))

	cases := []struct {
		name string
		mut  func(string) string
		want error
	}{
		{"missing auth strong", func(s string) string {
			return strings.Replace(s, "auth strong\n", "", 1)
		}, ErrNoAuthStrong},
		{"auth none", func(s string) string {
			return strings.Replace(s, "auth strong\n", "auth none\n", 1)
		}, ErrNoAuthStrong},
		{"missing noforce", func(s string) string {
			return strings.Replace(s, "noforce\n", "", 1)
		}, ErrNoNoforce},
		{"missing internal", func(s string) string {
			return strings.Replace(s, "internal 139.99.68.39\n", "", 1)
		}, ErrNoInternal},
		{"internal drifted", func(s string) string {
			return strings.Replace(s, "internal 139.99.68.39", "internal 10.0.0.1", 1)
		}, ErrNoInternal},
		{"missing trailing deny", func(s string) string {
			return strings.Replace(s, "deny *\n", "", 1)
		}, ErrNoDenyAll},
		{"allow after deny", func(s string) string {
			return strings.Replace(s, "deny *\n", "deny *\nallow cust_ab12cd34\n", 1)
		}, ErrNoDenyAll},
		{"missing flush", func(s string) string {
			return strings.Replace(s, "flush\n", "", 1)
		}, ErrNoFlush},
		{"anon flag -a1", func(s string) string {
			return strings.Replace(s, "socks -p21001 -a -4", "socks -p21001 -a1 -4", 1)
		}, ErrBadAnonFlag},
		{"anon flag -a2", func(s string) string {
			return strings.Replace(s, "proxy -p22001 -a -4", "proxy -p22001 -a2 -4", 1)
		}, ErrBadAnonFlag},
		{"anon flag missing", func(s string) string {
			return strings.Replace(s, "proxy -p22001 -a -4", "proxy -p22001 -4", 1)
		}, ErrBadAnonFlag},
		{"unquoted users line", func(s string) string {
			return strings.Replace(s, `users "cust_ab12cd34:CL:Kq7mZr2xTn9wLb4V"`,
				"users cust_ab12cd34:CL:Kq7mZr2xTn9wLb4V", 1)
		}, ErrUnquotedUser},
		{"timeouts with eight values", func(s string) string {
			return strings.Replace(s, "timeouts "+Timeouts, "timeouts 1 5 30 60 180 1800 15 60", 1)
		}, ErrBadTimeouts},
		{"timeouts with eleven values", func(s string) string {
			return strings.Replace(s, "timeouts "+Timeouts, "timeouts "+Timeouts+" 5", 1)
		}, ErrBadTimeouts},
		{"http_delete keyword", func(s string) string {
			return strings.Replace(s, "allow cust_ab12cd34\n",
				"allow cust_ab12cd34 * * 80,443 CONNECT,HTTP_DELETE\n", 1)
		}, ErrBadOperation},
		{"unknown keyword in a deny line", func(s string) string {
			return strings.Replace(s, "deny *\n", "deny * * * * HTTP_PATCH\n", 1)
		}, ErrBadOperation},
		{"setuid drifted from the slot", func(s string) string {
			return strings.Replace(s, "setuid 6101", "setuid 6102", 1)
		}, ErrUIDMismatch},
		{"setgid drifted from the slot", func(s string) string {
			return strings.Replace(s, "setgid 6100", "setgid 65534", 1)
		}, ErrUIDMismatch},
		{"logformat without %e", func(s string) string {
			return strings.Replace(s, " %T %e\"", " %T\"", 1)
		}, ErrNoLogFormat},
		{"service port drifted", func(s string) string {
			return strings.Replace(s, "socks -p21001", "socks -p21099", 1)
		}, ErrPortMismatch},
		{"user missing from the acl", func(s string) string {
			return strings.Replace(s, "allow cust_ab12cd34\n", "", 1)
		}, ErrNoUsers},
		{"daemonizes", func(s string) string {
			return "daemon\n" + s
		}, domain.ErrInvalid},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Verify([]byte(tc.mut(base)), sp)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Verify error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestVerifyAcceptsRenderedOutput(t *testing.T) {
	for _, sp := range allModeSpecs(t) {
		if err := Verify(mustRender(t, sp), sp); err != nil {
			t.Fatalf("auth mode %s: %v", sp.AuthMode, err)
		}
	}
}

func allModeSpecs(t *testing.T) []Spec {
	t.Helper()
	up := testSpec(t)

	ip := testSpec(t)
	ip.AuthMode = domain.AuthIPList
	ip.Users = nil
	ip.AuthIPs = []netip.Prefix{netip.MustParsePrefix("203.0.113.5/32")}

	both := testSpec(t)
	both.AuthMode = domain.AuthBoth
	both.AuthIPs = []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}

	return []Spec{up, ip, both}
}

func TestRenderIPListUsesIPOnlyAndStrong(t *testing.T) {
	sp := testSpec(t)
	sp.AuthMode = domain.AuthIPList
	sp.Users = nil
	sp.AuthIPs = []netip.Prefix{netip.MustParsePrefix("203.0.113.5/32")}
	out := string(mustRender(t, sp))
	if !strings.Contains(out, "auth iponly strong\n") {
		t.Fatalf("ip whitelist needs iponly and a strong backstop:\n%s", out)
	}
	if !strings.Contains(out, "allow * 203.0.113.5/32\n") {
		t.Fatalf("missing whitelist acl:\n%s", out)
	}
	if strings.Contains(out, "\nusers ") {
		t.Fatalf("iplist mode must not emit a users line:\n%s", out)
	}
}

func TestRenderFlushPrecedesEveryService(t *testing.T) {
	lines := Parse(mustRender(t, testSpec(t)))
	services := 0
	for i, l := range lines {
		if l.Cmd() != ServiceProxy && l.Cmd() != ServiceSocks {
			continue
		}
		services++
		sawFlush := false
		for j := i - 1; j >= 0; j-- {
			if lines[j].Cmd() == "flush" {
				sawFlush = true
				break
			}
			if lines[j].Cmd() == ServiceProxy || lines[j].Cmd() == ServiceSocks {
				break
			}
		}
		if !sawFlush {
			t.Fatalf("service on line %d has no flush before its acl block", l.Num)
		}
	}
	if services != 2 {
		t.Fatalf("expected 2 services, got %d", services)
	}
}

func TestRevokesAccess(t *testing.T) {
	sp := testSpec(t)
	base := mustRender(t, sp)

	same := mustRender(t, sp)
	if RevokesAccess(base, same) {
		t.Fatal("identical configs must reload, not restart")
	}

	added := sp
	added.Users = append(append([]User(nil), sp.Users...), User{Name: "cust2", Password: "Aa1Bb2Cc3Dd4Ee5F"})
	if RevokesAccess(base, mustRender(t, added)) {
		t.Fatal("adding a user must not force a restart")
	}

	rotated := sp
	rotated.Users = []User{{Name: "cust_ab12cd34", Password: "Zz9Yy8Xx7Ww6Vv5U"}}
	if !RevokesAccess(base, mustRender(t, rotated)) {
		t.Fatal("a password change must force a restart, noforce keeps the old session alive")
	}

	bothSpec := sp
	bothSpec.AuthMode = domain.AuthBoth
	bothSpec.AuthIPs = []netip.Prefix{
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.5/32"),
	}
	wide := mustRender(t, bothSpec)

	narrow := bothSpec
	narrow.AuthIPs = []netip.Prefix{netip.MustParsePrefix("203.0.113.5/32")}
	if !RevokesAccess(wide, mustRender(t, narrow)) {
		t.Fatal("removing a whitelisted network must force a restart")
	}
}

func TestParseIgnoresCommentsAndIndentedLines(t *testing.T) {
	lines := Parse([]byte("# comment\n\n  indented\nauth strong\n"))
	if len(lines) != 1 || lines[0].Cmd() != "auth" {
		t.Fatalf("unexpected parse: %+v", lines)
	}
}

func TestValidOperationsExcludesHTTPDelete(t *testing.T) {
	for _, op := range ValidOperations() {
		if op == "HTTP_DELETE" {
			t.Fatal("HTTP_DELETE is not a 3proxy keyword")
		}
	}
}
