package fw

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/n4darae/huawei-API/src/internal/config"
)

func render(t *testing.T) string {
	t.Helper()
	body, err := NewNft(Options{}).Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return string(body)
}

func TestRulesetNeverFlushesTheWholeRuleset(t *testing.T) {
	if strings.Contains(strings.ToLower(render(t)), forbiddenFlush) {
		t.Fatalf("a ruleset-wide flush would wipe the ufw and fail2ban chains and open the host silently")
	}
	raw, err := os.ReadFile("ruleset.nft.tmpl")
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	if strings.Contains(strings.ToLower(string(raw)), forbiddenFlush) {
		t.Fatal("the template itself must never contain a ruleset-wide flush")
	}
}

func TestTeardownIsScopedToOurTableOnly(t *testing.T) {
	body := render(t)
	deletes := regexp.MustCompile(`(?m)^\s*delete table .*$`).FindAllString(body, -1)
	if len(deletes) != 1 {
		t.Fatalf("want exactly one delete table, got %v", deletes)
	}
	want := "delete table " + config.NftFamily + " " + config.NftTable
	if strings.TrimSpace(deletes[0]) != want {
		t.Fatalf("got %q, want %q", strings.TrimSpace(deletes[0]), want)
	}
}

func TestSkgidIsNumeric(t *testing.T) {
	body := render(t)
	if !strings.Contains(body, "meta skgid 6100") {
		t.Fatal("nft resolves group names at load time; a missing group fails the whole ruleset while the unit reports success")
	}
	if strings.Contains(body, "meta skgid "+config.GroupName) {
		t.Fatal("the group name must never appear in a skgid match")
	}
}

func indexOfComment(t *testing.T, body, comment string) int {
	t.Helper()
	i := strings.Index(body, `comment "`+comment+`"`)
	if i < 0 {
		t.Fatalf("rule %q is missing from the ruleset", comment)
	}
	return i
}

func TestCustomerLegAcceptComesBeforeEveryDrop(t *testing.T) {
	body := render(t)
	customer := indexOfComment(t, body, CommentCustomerLeg)
	for _, later := range []string{"ssrf log", "ssrf drop", "leak log", "leak drop"} {
		if customer > indexOfComment(t, body, later) {
			t.Fatalf("the customer accept must precede %q or every SYN-ACK is swallowed", later)
		}
	}
}

func TestLoopbackLegIsAsTightAsTheCustomerLeg(t *testing.T) {
	body := render(t)
	var line string
	for _, l := range strings.Split(body, "\n") {
		if strings.Contains(l, `comment "`+CommentLoopbackLeg+`"`) {
			line = strings.TrimSpace(l)
		}
	}
	if line == "" {
		t.Fatal("the farm-local probe rule is missing")
	}
	for _, want := range []string{
		`oifname "lo"`,
		"ip saddr @" + SetPublicIPs,
		"tcp sport @" + SetProxyPorts,
		"ct state established,related",
		"counter",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("the loopback exception must keep %q or it becomes an escape hatch: %s", want, line)
		}
	}
	if strings.Contains(line, "ct state new") {
		t.Fatal("the loopback exception must never match a locally originated new connection")
	}
}

func TestLoopbackLegSitsBesideTheCustomerLegAndBeforeTheFence(t *testing.T) {
	body := render(t)
	customer := indexOfComment(t, body, CommentCustomerLeg)
	loopback := indexOfComment(t, body, CommentLoopbackLeg)
	fence := indexOfComment(t, body, "fence tcp reset")
	if !(customer < loopback && loopback < fence) {
		t.Fatalf("order must be customer leg, loopback leg, fence; got %d %d %d", customer, loopback, fence)
	}
	for _, later := range []string{"ssrf log", "ssrf drop", "leak log", "leak drop"} {
		if loopback > indexOfComment(t, body, later) {
			t.Fatalf("the loopback accept must precede %q", later)
		}
	}
}

func TestOnlyTheLoopbackRuleNamesAnInterfaceLiterally(t *testing.T) {
	body := render(t)
	var literal []string
	for _, l := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "#") || !strings.Contains(trimmed, `oifname "`) {
			continue
		}
		literal = append(literal, trimmed)
	}
	if len(literal) != 1 {
		t.Fatalf("interfaces are matched through sets so they can be asserted; only the loopback rule may name one literally, got %v", literal)
	}
	if !strings.Contains(literal[0], CommentLoopbackLeg) {
		t.Fatalf("unexpected literal interface match: %s", literal[0])
	}
}

func TestDNSAcceptPrecedesTheBlackhole(t *testing.T) {
	body := render(t)
	if indexOfComment(t, body, "dns to dongle gateway") > indexOfComment(t, body, "ssrf log") {
		t.Fatal("192.168.10N.1 is inside 192.168.0.0/16, so the gateway accept must come first")
	}
}

func TestFenceRulesPrecedeTheGenericDrops(t *testing.T) {
	body := render(t)
	fence := indexOfComment(t, body, "fence tcp reset")
	if fence > indexOfComment(t, body, "leak drop") {
		t.Fatal("fencing must be evaluated before the generic leak drop")
	}
	if indexOfComment(t, body, "fence tcp reset") > indexOfComment(t, body, "fence icmpx") {
		t.Fatal("the tcp reset must precede the icmpx fallback")
	}
}

func TestEveryRateLimitedDropHasAnUnconditionalDropBehindIt(t *testing.T) {
	body := render(t)
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || !strings.Contains(trimmed, "limit rate ") {
			continue
		}
		if strings.Contains(trimmed, "limit rate over ") {
			continue
		}
		if !strings.Contains(trimmed, "drop") {
			continue
		}
		matcher := strings.TrimSpace(trimmed[:strings.Index(trimmed, "limit rate ")])
		if !strings.Contains(body, matcher+" counter drop") {
			t.Fatalf("`limit rate` is a match, so packets over the rate fall through to the next rule; %q needs an unconditional drop behind it", matcher)
		}
	}
}

func TestEgressRuleOrderMatchesTheTemplate(t *testing.T) {
	body := render(t)
	found := regexp.MustCompile(`comment "([^"]+)"`).FindAllStringSubmatch(body, -1)
	var got []string
	for _, m := range found {
		got = append(got, m[1])
	}
	want := EgressRuleOrder()
	if len(got) != len(want) {
		t.Fatalf("template has %d commented rules %v, EgressRuleOrder has %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rule %d is %q in the template but %q in EgressRuleOrder", i, got[i], want[i])
		}
	}
}

func TestRulesetDeclaresEverySetTheContractNames(t *testing.T) {
	body := render(t)
	for _, s := range AllSets() {
		if !strings.Contains(body, "set "+s+" ") {
			t.Fatalf("set %s is missing from the ruleset", s)
		}
	}
	for _, c := range []string{ChainOutput, ChainEgress} {
		if !strings.Contains(body, "chain "+c+" {") {
			t.Fatalf("chain %s is missing from the ruleset", c)
		}
	}
}

func TestRulesetHasNoInputChain(t *testing.T) {
	body := render(t)
	if strings.Contains(body, "hook input") {
		t.Fatal("ingress belongs to ufw; an input chain here takes down every service on the host")
	}
	if strings.Contains(body, "policy drop") {
		t.Fatal("the output chain policy must stay accept")
	}
}

func TestProxyPortsCoverEverySlot(t *testing.T) {
	body := render(t)
	if !strings.Contains(body, "21000-22999") {
		t.Fatalf("proxy port range missing from:\n%s", body)
	}
}

type fakeNft struct {
	calls    []string
	sets     map[string][]string
	chainOut string
	tableOut string
	failAdd  bool
}

func newFakeNft() *fakeNft {
	return &fakeNft{sets: map[string][]string{}}
}

func (f *fakeNft) exec(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	full := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, full)
	switch {
	case len(args) > 0 && args[0] == "-f":
		return nil, nil
	case len(args) >= 2 && args[0] == "add" && args[1] == "element":
		if f.failAdd {
			return nil, &CommandError{Name: name, Args: args, Output: "Error: No such file or directory", Err: errors.New("exit status 1")}
		}
		set := args[4]
		element := strings.Trim(strings.Trim(args[5], "{} "), `"`)
		f.sets[set] = append(f.sets[set], element)
		return nil, nil
	case len(args) >= 2 && args[0] == "delete" && args[1] == "element":
		set := args[4]
		element := strings.Trim(strings.Trim(args[5], "{} "), `"`)
		kept := f.sets[set][:0]
		removed := false
		for _, m := range f.sets[set] {
			if m == element {
				removed = true
				continue
			}
			kept = append(kept, m)
		}
		f.sets[set] = kept
		if !removed {
			return nil, &CommandError{Name: name, Args: args, Output: "Error: Could not process rule: No such file or directory", Err: errors.New("exit status 1")}
		}
		return nil, nil
	case len(args) >= 3 && args[0] == "-j" && args[2] == "set":
		set := args[5]
		var elems []string
		for _, m := range f.sets[set] {
			elems = append(elems, `"`+m+`"`)
		}
		body := `{"nftables":[{"set":{"family":"inet","name":"` + set + `","table":"dongled"`
		if len(elems) > 0 {
			body += `,"elem":[` + strings.Join(elems, ",") + `]`
		}
		body += `}}]}`
		return []byte(body), nil
	case len(args) >= 3 && args[0] == "-j" && args[2] == "chain":
		return []byte(f.chainOut), nil
	case len(args) >= 3 && args[0] == "-j" && args[2] == "table":
		return []byte(f.tableOut), nil
	}
	return nil, nil
}

func TestAddDongleVerifiesMembershipRatherThanTrustingTheExitCode(t *testing.T) {
	f := newFakeNft()
	n := NewNft(Options{Exec: f.exec})
	ctx := context.Background()
	if err := n.AddDongle(ctx, "dg01", netip.MustParseAddr("192.168.101.1")); err != nil {
		t.Fatalf("AddDongle: %v", err)
	}
	if got := f.sets[SetDongleIfaces]; len(got) != 1 || got[0] != "dg01" {
		t.Fatalf("dongle_ifaces %v", got)
	}
	if got := f.sets[SetDongleGws]; len(got) != 1 || got[0] != "192.168.101.1" {
		t.Fatalf("dongle_gws %v", got)
	}
}

func TestAddDongleFailsWhenTheElementDoesNotStick(t *testing.T) {
	f := newFakeNft()
	n := NewNft(Options{Exec: f.exec})
	f.sets[SetDongleIfaces] = nil
	swallow := &fakeNft{sets: map[string][]string{}}
	swallow.exec(context.Background(), nil, "nft")
	n = NewNft(Options{Exec: func(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
		if len(args) >= 2 && args[0] == "add" && args[1] == "element" {
			return nil, nil
		}
		return swallow.exec(ctx, stdin, name, args...)
	}})
	err := n.AddDongle(context.Background(), "dg01", netip.MustParseAddr("192.168.101.1"))
	if !errors.Is(err, ErrElementMissing) {
		t.Fatalf("adding an element for a non-existent interface succeeds, so membership must be verified; got %v", err)
	}
}

func TestFenceAndUnfenceRoundTrip(t *testing.T) {
	f := newFakeNft()
	n := NewNft(Options{Exec: f.exec})
	ctx := context.Background()
	if err := n.AddDongle(ctx, "dg01", netip.MustParseAddr("192.168.101.1")); err != nil {
		t.Fatalf("AddDongle: %v", err)
	}
	if fenced, err := n.IsFenced(ctx, "dg01"); err != nil || fenced {
		t.Fatalf("IsFenced before: %v %v", fenced, err)
	}
	if err := n.Fence(ctx, "dg01"); err != nil {
		t.Fatalf("Fence: %v", err)
	}
	if fenced, err := n.IsFenced(ctx, "dg01"); err != nil || !fenced {
		t.Fatalf("IsFenced after fence: %v %v", fenced, err)
	}
	if err := n.Unfence(ctx, "dg01"); err != nil {
		t.Fatalf("Unfence: %v", err)
	}
	if fenced, err := n.IsFenced(ctx, "dg01"); err != nil || fenced {
		t.Fatalf("IsFenced after unfence: %v %v", fenced, err)
	}
}

func TestIsFencedReportsAnUnknownInterfaceAsAnError(t *testing.T) {
	f := newFakeNft()
	n := NewNft(Options{Exec: f.exec})
	ctx := context.Background()
	if _, err := n.IsFenced(ctx, "dg07"); !errors.Is(err, ErrUnknownIface) {
		t.Fatalf("an interface absent from dongle_ifaces must be distinguishable from a known unfenced one, else the reconciler never emits ActAddFwDongle and dongle_ifaces stays empty; got %v", err)
	}
	if err := n.AddDongle(ctx, "dg07", netip.MustParseAddr("192.168.107.1")); err != nil {
		t.Fatalf("AddDongle: %v", err)
	}
	fenced, err := n.IsFenced(ctx, "dg07")
	if err != nil || fenced {
		t.Fatalf("IsFenced after AddDongle: %v %v", fenced, err)
	}
}

func TestRemovingAnAbsentElementIsANoOp(t *testing.T) {
	f := newFakeNft()
	n := NewNft(Options{Exec: f.exec})
	ctx := context.Background()
	if err := n.Unfence(ctx, "dg09"); err != nil {
		t.Fatalf("unfencing an interface that was never fenced must be a no-op, got %v", err)
	}
	if err := n.RemoveDongle(ctx, "dg09"); err != nil {
		t.Fatalf("removing an unknown dongle must be a no-op, got %v", err)
	}
	if err := n.RemovePublic(ctx, "enp1s0f0", netip.MustParseAddr("139.99.68.39")); err != nil {
		t.Fatalf("removing an unknown public leg must be a no-op, got %v", err)
	}
}

func TestRemoveDongleDerivesTheGatewayFromTheSlot(t *testing.T) {
	f := newFakeNft()
	n := NewNft(Options{Exec: f.exec})
	ctx := context.Background()
	if err := n.AddDongle(ctx, "dg02", netip.MustParseAddr("192.168.102.1")); err != nil {
		t.Fatalf("AddDongle: %v", err)
	}
	if err := n.RemoveDongle(ctx, "dg02"); err != nil {
		t.Fatalf("RemoveDongle: %v", err)
	}
	if len(f.sets[SetDongleIfaces]) != 0 || len(f.sets[SetDongleGws]) != 0 {
		t.Fatalf("both the interface and its gateway must be removed: %v %v", f.sets[SetDongleIfaces], f.sets[SetDongleGws])
	}
}

func TestRejectsEmptyIfaceAndBadAddress(t *testing.T) {
	n := NewNft(Options{Exec: newFakeNft().exec})
	ctx := context.Background()
	if err := n.AddDongle(ctx, "", netip.MustParseAddr("192.168.101.1")); !errors.Is(err, ErrBadIface) {
		t.Fatalf("want ErrBadIface, got %v", err)
	}
	if err := n.AddDongle(ctx, "dg01", netip.Addr{}); !errors.Is(err, ErrBadAddr) {
		t.Fatalf("want ErrBadAddr, got %v", err)
	}
	if err := n.AddDongle(ctx, "dg01", netip.MustParseAddr("2001:db8::1")); !errors.Is(err, ErrBadAddr) {
		t.Fatalf("the gateway set is ipv4_addr, got %v", err)
	}
}

func TestCustomerAcceptHitsReadsTheCounter(t *testing.T) {
	f := newFakeNft()
	f.chainOut = `{"nftables":[
	  {"rule":{"chain":"proxy_egress","comment":"customer-facing leg","expr":[{"match":{}},{"counter":{"packets":17,"bytes":900}},{"accept":null}]}},
	  {"rule":{"chain":"proxy_egress","comment":"leak drop","expr":[{"counter":{"packets":3,"bytes":40}},{"drop":null}]}}
	]}`
	n := NewNft(Options{Exec: f.exec})
	got, err := n.CustomerAcceptHits(context.Background())
	if err != nil {
		t.Fatalf("CustomerAcceptHits: %v", err)
	}
	if got != 17 {
		t.Fatalf("want 17 packets, got %d", got)
	}
}

func TestCustomerAcceptHitsFailsWhenTheRuleIsGone(t *testing.T) {
	f := newFakeNft()
	f.chainOut = `{"nftables":[{"rule":{"chain":"proxy_egress","comment":"leak drop","expr":[]}}]}`
	n := NewNft(Options{Exec: f.exec})
	if _, err := n.CustomerAcceptHits(context.Background()); !errors.Is(err, ErrNoCustomerRule) {
		t.Fatalf("want ErrNoCustomerRule, got %v", err)
	}
}

func TestVerifyDetectsAMissingSet(t *testing.T) {
	f := newFakeNft()
	f.tableOut = `{"nftables":[{"chain":{"name":"output","table":"dongled"}},{"chain":{"name":"proxy_egress","table":"dongled"}}]}`
	n := NewNft(Options{Exec: f.exec})
	if err := n.Verify(context.Background()); !errors.Is(err, ErrSetMissing) {
		t.Fatalf("want ErrSetMissing, got %v", err)
	}
}

func TestVerifyDetectsAMisorderedChain(t *testing.T) {
	f := newFakeNft()
	var objs []string
	for _, s := range AllSets() {
		objs = append(objs, `{"set":{"name":"`+s+`","table":"dongled"}}`)
	}
	objs = append(objs, `{"chain":{"name":"output","table":"dongled"}}`, `{"chain":{"name":"proxy_egress","table":"dongled"}}`)
	order := EgressRuleOrder()
	order[0], order[len(order)-2] = order[len(order)-2], order[0]
	for _, c := range order {
		objs = append(objs, `{"rule":{"chain":"proxy_egress","comment":"`+c+`","expr":[]}}`)
	}
	f.tableOut = `{"nftables":[` + strings.Join(objs, ",") + `]}`
	n := NewNft(Options{Exec: f.exec})
	if err := n.Verify(context.Background()); !errors.Is(err, ErrRuleOrder) {
		t.Fatalf("want ErrRuleOrder, got %v", err)
	}
}

func TestVerifyAcceptsTheRealOrder(t *testing.T) {
	f := newFakeNft()
	var objs []string
	for _, s := range AllSets() {
		objs = append(objs, `{"set":{"name":"`+s+`","table":"dongled"}}`)
	}
	objs = append(objs, `{"chain":{"name":"output","table":"dongled"}}`, `{"chain":{"name":"proxy_egress","table":"dongled"}}`)
	for _, c := range EgressRuleOrder() {
		objs = append(objs, `{"rule":{"chain":"proxy_egress","comment":"`+c+`","expr":[]}}`)
	}
	f.tableOut = `{"nftables":[` + strings.Join(objs, ",") + `]}`
	n := NewNft(Options{Exec: f.exec})
	if err := n.Verify(context.Background()); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestDecodeElementHandlesPrefixesAndRanges(t *testing.T) {
	if got := decodeElement([]byte(`"dg01"`)); got != "dg01" {
		t.Fatalf("plain string %q", got)
	}
	if got := decodeElement([]byte(`{"prefix":{"addr":"10.0.0.0","len":8}}`)); got != "10.0.0.0/8" {
		t.Fatalf("prefix %q", got)
	}
	if got := decodeElement([]byte(`{"range":["21000","22999"]}`)); got != "21000-22999" {
		t.Fatalf("range %q", got)
	}
}
