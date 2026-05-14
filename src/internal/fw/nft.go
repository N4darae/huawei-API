package fw

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"text/template"

	"github.com/n4darae/huawei-API/src/internal/config"
	"github.com/n4darae/huawei-API/src/internal/domain"
)

//go:embed ruleset.nft.tmpl
var rulesetTemplateSource string

var rulesetTemplate = template.Must(template.New("ruleset").Parse(rulesetTemplateSource))

type Options struct {
	Family      string
	Table       string
	GID         int
	NftPath     string
	Exec        Exec
	ProxyPortLo int
	ProxyPortHi int
}

type Nft struct {
	family      string
	table       string
	gid         int
	nft         string
	exec        Exec
	proxyPortLo int
	proxyPortHi int
}

var _ Firewall = (*Nft)(nil)

func NewNft(o Options) *Nft {
	if o.Family == "" {
		o.Family = config.NftFamily
	}
	if o.Table == "" {
		o.Table = config.NftTable
	}
	if o.GID == 0 {
		o.GID = config.GroupGID
	}
	if o.NftPath == "" {
		o.NftPath = "nft"
	}
	if o.Exec == nil {
		o.Exec = SystemExec
	}
	if o.ProxyPortLo == 0 {
		o.ProxyPortLo = config.ProxyPortLo
	}
	if o.ProxyPortHi == 0 {
		o.ProxyPortHi = config.ProxyPortHi
	}
	return &Nft{
		family:      o.Family,
		table:       o.Table,
		gid:         o.GID,
		nft:         o.NftPath,
		exec:        o.Exec,
		proxyPortLo: o.ProxyPortLo,
		proxyPortHi: o.ProxyPortHi,
	}
}

type rulesetView struct {
	Family             string
	Table              string
	GID                int
	ProxyPortLo        int
	ProxyPortHi        int
	ChainOutput        string
	ChainEgress        string
	SetDongleIfaces    string
	SetFencedIfaces    string
	SetDongleGws       string
	SetPublicIfaces    string
	SetPublicIPs       string
	SetProxyPorts      string
	SetBlackhole4      string
	CommentCustomerLeg string
	CommentLoopbackLeg string
	LogPrefixSSRF      string
	LogPrefixLeak      string
}

func (n *Nft) Render() ([]byte, error) {
	v := rulesetView{
		Family:             n.family,
		Table:              n.table,
		GID:                n.gid,
		ProxyPortLo:        n.proxyPortLo,
		ProxyPortHi:        n.proxyPortHi,
		ChainOutput:        ChainOutput,
		ChainEgress:        ChainEgress,
		SetDongleIfaces:    SetDongleIfaces,
		SetFencedIfaces:    SetFencedIfaces,
		SetDongleGws:       SetDongleGws,
		SetPublicIfaces:    SetPublicIfaces,
		SetPublicIPs:       SetPublicIPs,
		SetProxyPorts:      SetProxyPorts,
		SetBlackhole4:      SetBlackhole4,
		CommentCustomerLeg: CommentCustomerLeg,
		CommentLoopbackLeg: CommentLoopbackLeg,
		LogPrefixSSRF:      LogPrefixSSRF,
		LogPrefixLeak:      LogPrefixLeak,
	}
	var buf bytes.Buffer
	if err := rulesetTemplate.Execute(&buf, v); err != nil {
		return nil, err
	}
	if bytes.Contains(bytes.ToLower(buf.Bytes()), []byte(forbiddenFlush)) {
		return nil, ErrRulesetFlush
	}
	return buf.Bytes(), nil
}

func (n *Nft) EnsureTable(ctx context.Context) error {
	body, err := n.Render()
	if err != nil {
		return err
	}
	if _, err := n.exec(ctx, body, n.nft, "-f", "-"); err != nil {
		return err
	}
	return n.Verify(ctx)
}

func (n *Nft) Verify(ctx context.Context) error {
	out, err := n.exec(ctx, nil, n.nft, "-j", "list", "table", n.family, n.table)
	if err != nil {
		if IsAbsent(err) {
			return fmt.Errorf("%w: %s %s", ErrTableMissing, n.family, n.table)
		}
		return err
	}
	doc, err := decodeNft(out)
	if err != nil {
		return err
	}
	sets := map[string]bool{}
	chains := map[string]bool{}
	for _, obj := range doc.Nftables {
		if obj.Set != nil {
			sets[obj.Set.Name] = true
		}
		if obj.Chain != nil {
			chains[obj.Chain.Name] = true
		}
	}
	for _, want := range AllSets() {
		if !sets[want] {
			return fmt.Errorf("%w: %s", ErrSetMissing, want)
		}
	}
	for _, want := range []string{ChainOutput, ChainEgress} {
		if !chains[want] {
			return fmt.Errorf("%w: %s", ErrChainMissing, want)
		}
	}
	return n.verifyEgressOrder(doc)
}

func (n *Nft) verifyEgressOrder(doc *nftDoc) error {
	var got []string
	for _, obj := range doc.Nftables {
		if obj.Rule == nil || obj.Rule.Chain != ChainEgress {
			continue
		}
		got = append(got, obj.Rule.Comment)
	}
	want := EgressRuleOrder()
	if len(got) != len(want) {
		return fmt.Errorf("%w: got %d rules %v, want %d", ErrRuleOrder, len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			return fmt.Errorf("%w: rule %d is %q, want %q", ErrRuleOrder, i, got[i], want[i])
		}
	}
	return nil
}

func (n *Nft) AddPublic(ctx context.Context, iface string, ip netip.Addr) error {
	if err := validIface(iface); err != nil {
		return err
	}
	if err := validV4(ip); err != nil {
		return err
	}
	if err := n.addElement(ctx, SetPublicIfaces, quote(iface)); err != nil {
		return err
	}
	if err := n.addElement(ctx, SetPublicIPs, ip.String()); err != nil {
		return err
	}
	if err := n.mustContain(ctx, SetPublicIfaces, iface); err != nil {
		return err
	}
	return n.mustContain(ctx, SetPublicIPs, ip.String())
}

func (n *Nft) RemovePublic(ctx context.Context, iface string, ip netip.Addr) error {
	if iface != "" {
		if err := n.delElement(ctx, SetPublicIfaces, quote(iface)); err != nil {
			return err
		}
	}
	if ip.IsValid() {
		if err := n.delElement(ctx, SetPublicIPs, ip.String()); err != nil {
			return err
		}
	}
	return nil
}

func (n *Nft) AddDongle(ctx context.Context, iface string, gw netip.Addr) error {
	if err := validIface(iface); err != nil {
		return err
	}
	if err := validV4(gw); err != nil {
		return err
	}
	if err := n.addElement(ctx, SetDongleIfaces, quote(iface)); err != nil {
		return err
	}
	if err := n.addElement(ctx, SetDongleGws, gw.String()); err != nil {
		return err
	}
	if err := n.mustContain(ctx, SetDongleIfaces, iface); err != nil {
		return err
	}
	return n.mustContain(ctx, SetDongleGws, gw.String())
}

func (n *Nft) RemoveDongle(ctx context.Context, iface string) error {
	if err := validIface(iface); err != nil {
		return err
	}
	if err := n.delElement(ctx, SetDongleIfaces, quote(iface)); err != nil {
		return err
	}
	if err := n.delElement(ctx, SetFencedIfaces, quote(iface)); err != nil {
		return err
	}
	if s, ok := domain.ParseIfaceName(iface); ok {
		if err := n.delElement(ctx, SetDongleGws, s.GatewayIP().String()); err != nil {
			return err
		}
	}
	return nil
}

func (n *Nft) Fence(ctx context.Context, iface string) error {
	if err := validIface(iface); err != nil {
		return err
	}
	if err := n.addElement(ctx, SetFencedIfaces, quote(iface)); err != nil {
		return err
	}
	return n.mustContain(ctx, SetFencedIfaces, iface)
}

func (n *Nft) Unfence(ctx context.Context, iface string) error {
	if err := validIface(iface); err != nil {
		return err
	}
	if err := n.delElement(ctx, SetFencedIfaces, quote(iface)); err != nil {
		return err
	}
	members, err := n.SetMembers(ctx, SetFencedIfaces)
	if err != nil {
		return err
	}
	for _, m := range members {
		if m == iface {
			return fmt.Errorf("%w: %s is still fenced", ErrElementMissing, iface)
		}
	}
	return nil
}

func (n *Nft) IsFenced(ctx context.Context, iface string) (bool, error) {
	known, err := n.SetMembers(ctx, SetDongleIfaces)
	if err != nil {
		return false, err
	}
	if !slices.Contains(known, iface) {
		return false, ErrUnknownIface
	}
	members, err := n.SetMembers(ctx, SetFencedIfaces)
	if err != nil {
		return false, err
	}
	return slices.Contains(members, iface), nil
}

func (n *Nft) CustomerAcceptHits(ctx context.Context) (uint64, error) {
	return n.RuleHits(ctx, CommentCustomerLeg)
}

func (n *Nft) RuleHits(ctx context.Context, comment string) (uint64, error) {
	out, err := n.exec(ctx, nil, n.nft, "-j", "list", "chain", n.family, n.table, ChainEgress)
	if err != nil {
		return 0, err
	}
	doc, err := decodeNft(out)
	if err != nil {
		return 0, err
	}
	for _, obj := range doc.Nftables {
		if obj.Rule == nil || obj.Rule.Comment != comment {
			continue
		}
		for _, e := range obj.Rule.Expr {
			var wrapper struct {
				Counter *struct {
					Packets uint64 `json:"packets"`
				} `json:"counter"`
			}
			if err := json.Unmarshal(e, &wrapper); err != nil {
				continue
			}
			if wrapper.Counter != nil {
				return wrapper.Counter.Packets, nil
			}
		}
		return 0, nil
	}
	return 0, fmt.Errorf("%w: %s", ErrNoCustomerRule, comment)
}

func (n *Nft) SetMembers(ctx context.Context, set string) ([]string, error) {
	out, err := n.exec(ctx, nil, n.nft, "-j", "list", "set", n.family, n.table, set)
	if err != nil {
		if IsAbsent(err) {
			return nil, fmt.Errorf("%w: %s", ErrSetMissing, set)
		}
		return nil, err
	}
	doc, err := decodeNft(out)
	if err != nil {
		return nil, err
	}
	for _, obj := range doc.Nftables {
		if obj.Set == nil || obj.Set.Name != set {
			continue
		}
		members := make([]string, 0, len(obj.Set.Elem))
		for _, raw := range obj.Set.Elem {
			members = append(members, decodeElement(raw))
		}
		return members, nil
	}
	return nil, fmt.Errorf("%w: %s", ErrSetMissing, set)
}

func (n *Nft) mustContain(ctx context.Context, set, want string) error {
	members, err := n.SetMembers(ctx, set)
	if err != nil {
		return err
	}
	for _, m := range members {
		if m == want {
			return nil
		}
	}
	return fmt.Errorf("%w: %s not in %s (have %v)", ErrElementMissing, want, set, members)
}

func (n *Nft) addElement(ctx context.Context, set, element string) error {
	_, err := n.exec(ctx, nil, n.nft, "add", "element", n.family, n.table, set, "{ "+element+" }")
	return err
}

func (n *Nft) delElement(ctx context.Context, set, element string) error {
	_, err := n.exec(ctx, nil, n.nft, "delete", "element", n.family, n.table, set, "{ "+element+" }")
	return IgnoreAbsent(err)
}

func (n *Nft) ResetRules(ctx context.Context) error {
	_, err := n.exec(ctx, nil, n.nft, "reset", "rules", "table", n.family, n.table)
	return err
}

func quote(s string) string { return strconv.Quote(s) }

func validIface(s string) error {
	if strings.TrimSpace(s) == "" {
		return ErrBadIface
	}
	return nil
}

func validV4(a netip.Addr) error {
	if !a.IsValid() || !a.Is4() {
		return fmt.Errorf("%w: %s", ErrBadAddr, a)
	}
	return nil
}

type nftDoc struct {
	Nftables []nftObject `json:"nftables"`
}

type nftObject struct {
	Set   *nftSet   `json:"set"`
	Chain *nftChain `json:"chain"`
	Rule  *nftRule  `json:"rule"`
}

type nftSet struct {
	Name  string            `json:"name"`
	Table string            `json:"table"`
	Elem  []json.RawMessage `json:"elem"`
}

type nftChain struct {
	Name  string `json:"name"`
	Table string `json:"table"`
}

type nftRule struct {
	Chain   string            `json:"chain"`
	Comment string            `json:"comment"`
	Expr    []json.RawMessage `json:"expr"`
}

func decodeNft(b []byte) (*nftDoc, error) {
	var doc nftDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("fw: decoding nft json: %w", err)
	}
	return &doc, nil
}

func decodeElement(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var prefix struct {
		Prefix *struct {
			Addr string `json:"addr"`
			Len  int    `json:"len"`
		} `json:"prefix"`
		Range []string `json:"range"`
	}
	if err := json.Unmarshal(raw, &prefix); err == nil {
		if prefix.Prefix != nil {
			return prefix.Prefix.Addr + "/" + strconv.Itoa(prefix.Prefix.Len)
		}
		if len(prefix.Range) == 2 {
			return prefix.Range[0] + "-" + prefix.Range[1]
		}
	}
	return string(raw)
}
