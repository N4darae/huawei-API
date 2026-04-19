package proxysup

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/n4darae/huawei-API/src/internal/config"
)

func real3proxy(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("DONGLED_3PROXY_BIN")
	if bin == "" {
		bin = config.Bin3proxy
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("pinned 3proxy binary not present at %s: build it with third_party/3proxy/build.sh", bin)
	}
	return bin
}

func TestScratchConfigRewritesInternalAndPorts(t *testing.T) {
	sp := testSpec(t)
	cfg := mustRender(t, sp)

	out, services := scratchConfig(cfg, config.ProxyValidatePort)
	if services != 2 {
		t.Fatalf("rewrote %d services, want 2", services)
	}
	s := string(out)
	if !strings.Contains(s, "internal 127.0.0.1\n") {
		t.Fatalf("internal was not rewritten:\n%s", s)
	}
	if strings.Contains(s, "internal 139.99.68.39") {
		t.Fatalf("public bind survived the rewrite:\n%s", s)
	}
	if strings.Contains(s, "-p21001") || strings.Contains(s, "-p22001") {
		t.Fatalf("live ports survived the rewrite:\n%s", s)
	}
	if !strings.Contains(s, "socks -p20999 -a -4") || !strings.Contains(s, "proxy -p20999 -a -4") {
		t.Fatalf("scratch port missing:\n%s", s)
	}
	if !strings.Contains(s, "external 192.168.101.100\n") {
		t.Fatalf("external must survive, it never binds at startup:\n%s", s)
	}
}

func TestParseProcNetTCPFindsListeners(t *testing.T) {
	raw := "" +
		"  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		"   0: 0100007F:5207 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 12345 1 0 0 10 0\n" +
		"   1: 2744638B:5209 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 12346 1 0 0 10 0\n" +
		"   2: 0100007F:520A 0100007F:8000 01 00000000:00000000 00:00000000 00000000  1000        0 12347 1 0 0 10 0\n"

	got := parseProcNetTCP([]byte(raw))
	if len(got) != 2 {
		t.Fatalf("parsed %v, want two listeners", got)
	}
	if got[0] != netip.MustParseAddrPort("127.0.0.1:20999") {
		t.Fatalf("first listener %v", got[0])
	}
	if got[1] != netip.MustParseAddrPort("139.99.68.39:21001") {
		t.Fatalf("second listener %v", got[1])
	}
}

func TestValidateRefusesAnEmptyRequest(t *testing.T) {
	if _, err := Validate(context.Background(), ValidateRequest{}); !errors.Is(err, ErrValidateFailed) {
		t.Fatalf("err = %v, want ErrValidateFailed", err)
	}
}

func TestValidateWithoutNetnsIsRefusedUnlessFallbackIsAllowed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can create namespaces, so there is nothing to refuse")
	}
	_, err := Validate(context.Background(), ValidateRequest{
		Bin:    real3proxy(t),
		Config: mustRender(t, testSpec(t)),
		Spec:   testSpec(t),
	})
	if !errors.Is(err, ErrNoNetns) {
		t.Fatalf("err = %v, want ErrNoNetns", err)
	}
}

func scratchSpec(t *testing.T) Spec {
	t.Helper()
	sp := testSpec(t)
	sp.LogPath = t.TempDir() + "/px01.log"
	return sp
}

func unprivilegedConfig(t *testing.T, sp Spec) []byte {
	t.Helper()
	cfg := mustRender(t, sp)
	out := []string{}
	for _, l := range strings.Split(string(cfg), "\n") {
		if strings.HasPrefix(l, "setuid ") || strings.HasPrefix(l, "setgid ") {
			continue
		}
		out = append(out, l)
	}
	return []byte(strings.Join(out, "\n"))
}

func TestValidateScratchAcceptsARealConfig(t *testing.T) {
	sp := scratchSpec(t)
	rep, err := Validate(context.Background(), ValidateRequest{
		Bin:       real3proxy(t),
		Config:    unprivilegedConfig(t, sp),
		Spec:      sp,
		Timeout:   3 * time.Second,
		ForceMode: ValidateScratch,
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if rep.Mode != ValidateScratch || !rep.Degraded {
		t.Fatalf("scratch mode must announce itself as degraded: %+v", rep)
	}
	if !strings.Contains(rep.Note, "exact bytes") {
		t.Fatalf("scratch mode must say it did not validate the installed bytes: %q", rep.Note)
	}
	if len(rep.BoundPorts) < 2 {
		t.Fatalf("expected both services on the scratch port, got %v", rep.BoundPorts)
	}
}

func TestValidateScratchRejectsAnUnknownOperationKeyword(t *testing.T) {
	sp := scratchSpec(t)
	broken := strings.Replace(string(unprivilegedConfig(t, sp)),
		"allow cust_ab12cd34\n", "allow cust_ab12cd34 * * 80,443 CONNECT,HTTP_DELETE\n", 1)

	rep, err := Validate(context.Background(), ValidateRequest{
		Bin:       real3proxy(t),
		Config:    []byte(broken),
		Spec:      sp,
		Timeout:   3 * time.Second,
		ForceMode: ValidateScratch,
	})
	if !errors.Is(err, ErrValidateFailed) {
		t.Fatalf("err = %v, want ErrValidateFailed", err)
	}
	if !strings.Contains(err.Error(), "exact bytes") {
		t.Fatalf("scratch failures must carry the caveat: %v", err)
	}
	if !strings.Contains(rep.Stderr, "Unknown operation type") {
		t.Fatalf("stderr must explain the refusal: %q", rep.Stderr)
	}
}

func TestValidateScratchRejectsATimeoutsLineThatKillsTheParse(t *testing.T) {
	sp := scratchSpec(t)
	broken := strings.Replace(string(unprivilegedConfig(t, sp)),
		"allow cust_ab12cd34\n", "allow cust_ab12cd34 * * * NOT_A_KEYWORD\n", 1)

	if _, err := Validate(context.Background(), ValidateRequest{
		Bin:       real3proxy(t),
		Config:    []byte(broken),
		Spec:      sp,
		Timeout:   3 * time.Second,
		ForceMode: ValidateScratch,
	}); !errors.Is(err, ErrValidateFailed) {
		t.Fatalf("err = %v, want ErrValidateFailed", err)
	}
}

func TestPinnedCommitMatchesTheBuildScript(t *testing.T) {
	raw, err := os.ReadFile("../../third_party/3proxy/VERSION")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), config.Pin3proxyCommit) {
		t.Fatalf("VERSION does not pin %s:\n%s", config.Pin3proxyCommit, raw)
	}
	if !strings.Contains(string(raw), "makefile=Makefile.Linux") {
		t.Fatalf("VERSION does not pin the linux makefile:\n%s", raw)
	}
	script, err := os.ReadFile("../../third_party/3proxy/build.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"WOLFSSL_CHECK=false",
		"OPENSSL_CHECK=false",
		"PCRE_CHECK=false",
		"PAM_CHECK=false",
		"src/resolve.c",
		"rev-parse HEAD",
	} {
		if !strings.Contains(string(script), want) {
			t.Fatalf("build.sh is missing %q", want)
		}
	}
}
