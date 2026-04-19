package linux

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/n4darae/huawei-API/src/internal/netcfg"
)

const (
	iflaAddress   = 1
	iflaIfname    = 3
	iflaMTU       = 4
	iflaOperState = 16
	iflaCarrier   = 33

	ifaAddress = 1
	ifaLocal   = 2

	fraDst      = 1
	fraSrc      = 2
	fraIifName  = 3
	fraPriority = 6
	fraTable    = 15
	fraOifName  = 17
	fraUIDRange = 20

	rtaDst     = 1
	rtaOif     = 4
	rtaGateway = 5
	rtaPrefSrc = 7
	rtaTable   = 15

	frActToTbl       = 1
	frActGoto        = 2
	frActNop         = 3
	frActBlackhole   = 6
	frActUnreachable = 7
	frActProhibit    = 8

	rtTableMain    = 254
	rtTableLocal   = 255
	rtTableDefault = 253

	sizeofIfInfomsg  = 16
	sizeofIfAddrmsg  = 8
	sizeofFibRuleHdr = 12
	sizeofRtMsg      = 12
	sizeofNlAttr     = 4

	rtmGetRule = 0x22

	rtnlGroupLink = 1
)

var operStateNames = map[uint8]string{
	0: netcfg.OperStateUnknown,
	1: "notpresent",
	2: netcfg.OperStateDown,
	3: "lowerlayerdown",
	4: "testing",
	5: "dormant",
	6: netcfg.OperStateUp,
}

var ruleActionNames = map[uint8]string{
	frActToTbl:       "lookup",
	frActGoto:        "goto",
	frActNop:         "nop",
	frActBlackhole:   "blackhole",
	frActUnreachable: "unreachable",
	frActProhibit:    "prohibit",
}

type attr struct {
	Type uint16
	Data []byte
}

func parseAttrs(b []byte) ([]attr, error) {
	var out []attr
	for len(b) >= sizeofNlAttr {
		length := int(binary.NativeEndian.Uint16(b[0:2]))
		kind := binary.NativeEndian.Uint16(b[2:4])
		if length < sizeofNlAttr || length > len(b) {
			return nil, netcfg.ErrMalformedNetlink
		}
		out = append(out, attr{Type: kind & 0x3fff, Data: b[sizeofNlAttr:length]})
		aligned := (length + 3) &^ 3
		if aligned >= len(b) {
			break
		}
		b = b[aligned:]
	}
	return out, nil
}

func attrString(a attr) string {
	return strings.TrimRight(string(a.Data), "\x00")
}

func attrU32(a attr) (uint32, bool) {
	if len(a.Data) < 4 {
		return 0, false
	}
	return binary.NativeEndian.Uint32(a.Data[0:4]), true
}

func attrAddr(a attr) (netip.Addr, bool) {
	switch len(a.Data) {
	case 4:
		return netip.AddrFrom4([4]byte(a.Data[0:4])), true
	case 16:
		return netip.AddrFrom16([16]byte(a.Data[0:16])).Unmap(), true
	default:
		return netip.Addr{}, false
	}
}

func attrMAC(a attr) string {
	if len(a.Data) != 6 {
		return ""
	}
	parts := make([]string, 6)
	for i, b := range a.Data {
		parts[i] = fmt.Sprintf("%02x", b)
	}
	return strings.Join(parts, ":")
}

func dump(proto uint16, payload []byte) ([]syscall.NetlinkMessage, error) {
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, syscall.NETLINK_ROUTE)
	if err != nil {
		return nil, err
	}
	defer syscall.Close(fd)
	lsa := &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}
	if err := syscall.Bind(fd, lsa); err != nil {
		return nil, err
	}
	req := make([]byte, syscall.NLMSG_HDRLEN+len(payload))
	binary.NativeEndian.PutUint32(req[0:4], uint32(len(req)))
	binary.NativeEndian.PutUint16(req[4:6], proto)
	binary.NativeEndian.PutUint16(req[6:8], syscall.NLM_F_REQUEST|syscall.NLM_F_DUMP)
	binary.NativeEndian.PutUint32(req[8:12], 1)
	copy(req[syscall.NLMSG_HDRLEN:], payload)
	if err := syscall.Sendto(fd, req, 0, lsa); err != nil {
		return nil, err
	}
	var out []syscall.NetlinkMessage
	for {
		buf := make([]byte, 1<<17)
		n, _, err := syscall.Recvfrom(fd, buf, 0)
		if err != nil {
			return nil, err
		}
		if n < syscall.NLMSG_HDRLEN {
			return nil, netcfg.ErrMalformedNetlink
		}
		msgs, err := syscall.ParseNetlinkMessage(buf[:n])
		if err != nil {
			return nil, err
		}
		done := false
		for _, m := range msgs {
			switch m.Header.Type {
			case syscall.NLMSG_DONE:
				done = true
			case syscall.NLMSG_ERROR:
				return nil, netlinkError(m.Data)
			default:
				out = append(out, m)
			}
		}
		if done {
			break
		}
	}
	return out, nil
}

func netlinkError(data []byte) error {
	if len(data) < 4 {
		return netcfg.ErrMalformedNetlink
	}
	code := int32(binary.NativeEndian.Uint32(data[0:4]))
	if code == 0 {
		return nil
	}
	return syscall.Errno(-code)
}

type Observer struct {
	Exec     netcfg.Exec
	ProcRoot string
	SysRoot  string
}

func NewObserver(e netcfg.Exec) *Observer {
	if e == nil {
		e = netcfg.SystemExec
	}
	return &Observer{Exec: e, ProcRoot: "/proc", SysRoot: "/sys"}
}

func (o *Observer) Links(ctx context.Context) (map[string]netcfg.LinkState, error) {
	payload := make([]byte, sizeofIfInfomsg)
	payload[0] = syscall.AF_UNSPEC
	msgs, err := dump(syscall.RTM_GETLINK, payload)
	if err != nil {
		return nil, err
	}
	byIndex := map[int]string{}
	links := map[string]netcfg.LinkState{}
	for _, m := range msgs {
		if m.Header.Type != syscall.RTM_NEWLINK || len(m.Data) < sizeofIfInfomsg {
			continue
		}
		index := int(int32(binary.NativeEndian.Uint32(m.Data[4:8])))
		attrs, err := parseAttrs(m.Data[sizeofIfInfomsg:])
		if err != nil {
			return nil, err
		}
		st := netcfg.LinkState{Index: index, OperState: netcfg.OperStateUnknown}
		for _, a := range attrs {
			switch a.Type {
			case iflaIfname:
				st.Name = attrString(a)
			case iflaAddress:
				st.MAC = attrMAC(a)
			case iflaMTU:
				if v, ok := attrU32(a); ok {
					st.MTU = int(v)
				}
			case iflaOperState:
				if len(a.Data) > 0 {
					if name, ok := operStateNames[a.Data[0]]; ok {
						st.OperState = name
					}
				}
			case iflaCarrier:
				if len(a.Data) > 0 {
					st.Carrier = a.Data[0] != 0
				}
			}
		}
		if st.Name == "" {
			continue
		}
		byIndex[index] = st.Name
		links[st.Name] = st
	}
	addrs, err := o.addrs(ctx)
	if err != nil {
		return nil, err
	}
	for index, list := range addrs {
		name, ok := byIndex[index]
		if !ok {
			continue
		}
		st := links[name]
		st.Addrs = list
		links[name] = st
	}
	for name, st := range links {
		st.IDPath = o.idPath(ctx, name)
		links[name] = st
	}
	return links, nil
}

func (o *Observer) addrs(ctx context.Context) (map[int][]netip.Prefix, error) {
	payload := make([]byte, sizeofIfAddrmsg)
	payload[0] = syscall.AF_UNSPEC
	msgs, err := dump(syscall.RTM_GETADDR, payload)
	if err != nil {
		return nil, err
	}
	out := map[int][]netip.Prefix{}
	for _, m := range msgs {
		if m.Header.Type != syscall.RTM_NEWADDR || len(m.Data) < sizeofIfAddrmsg {
			continue
		}
		prefixLen := int(m.Data[1])
		index := int(binary.NativeEndian.Uint32(m.Data[4:8]))
		attrs, err := parseAttrs(m.Data[sizeofIfAddrmsg:])
		if err != nil {
			return nil, err
		}
		var addr netip.Addr
		for _, a := range attrs {
			if a.Type != ifaLocal && a.Type != ifaAddress {
				continue
			}
			got, ok := attrAddr(a)
			if !ok {
				continue
			}
			if a.Type == ifaLocal || !addr.IsValid() {
				addr = got
			}
		}
		if !addr.IsValid() {
			continue
		}
		out[index] = append(out[index], netip.PrefixFrom(addr, prefixLen))
	}
	return out, nil
}

func (o *Observer) idPath(ctx context.Context, iface string) string {
	if o.Exec == nil {
		return ""
	}
	out, err := o.Exec(ctx, "udevadm", "info", "--query=property", "--path=/sys/class/net/"+iface)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "ID_PATH="); ok {
			return v
		}
	}
	return ""
}

func (o *Observer) Rules(ctx context.Context) ([]netcfg.RuleState, error) {
	payload := make([]byte, sizeofFibRuleHdr)
	payload[0] = syscall.AF_UNSPEC
	msgs, err := dump(rtmGetRule, payload)
	if err != nil {
		return nil, err
	}
	var out []netcfg.RuleState
	for _, m := range msgs {
		if len(m.Data) < sizeofFibRuleHdr {
			continue
		}
		dstLen := int(m.Data[1])
		srcLen := int(m.Data[2])
		table := int(m.Data[4])
		action := m.Data[7]
		attrs, err := parseAttrs(m.Data[sizeofFibRuleHdr:])
		if err != nil {
			return nil, err
		}
		r := netcfg.RuleState{Table: table, Action: ruleActionNames[action], UIDRangeLo: -1, UIDRangeHi: -1}
		if r.Action == "" {
			r.Action = strconv.Itoa(int(action))
		}
		for _, a := range attrs {
			switch a.Type {
			case fraPriority:
				if v, ok := attrU32(a); ok {
					r.Priority = int(v)
				}
			case fraTable:
				if v, ok := attrU32(a); ok {
					r.Table = int(v)
				}
			case fraSrc:
				if addr, ok := attrAddr(a); ok {
					r.Src = netip.PrefixFrom(addr, srcLen)
				}
			case fraDst:
				if addr, ok := attrAddr(a); ok {
					r.Dst = netip.PrefixFrom(addr, dstLen)
				}
			case fraIifName:
				r.IifName = attrString(a)
			case fraOifName:
				_ = attrString(a)
			case fraUIDRange:
				if len(a.Data) >= 8 {
					r.UIDRangeLo = int(binary.NativeEndian.Uint32(a.Data[0:4]))
					r.UIDRangeHi = int(binary.NativeEndian.Uint32(a.Data[4:8]))
				}
			}
		}
		r.Raw = formatRule(r)
		out = append(out, r)
	}
	return out, nil
}

func formatRule(r netcfg.RuleState) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d:", r.Priority)
	if r.Src.IsValid() {
		fmt.Fprintf(&b, " from %s", r.Src)
	} else {
		b.WriteString(" from all")
	}
	if r.IifName != "" {
		fmt.Fprintf(&b, " iif %s", r.IifName)
	}
	if r.UIDRangeLo >= 0 {
		fmt.Fprintf(&b, " uidrange %d-%d", r.UIDRangeLo, r.UIDRangeHi)
	}
	fmt.Fprintf(&b, " %s %s", r.Action, tableName(r.Table))
	return b.String()
}

func tableName(t int) string {
	switch t {
	case rtTableMain:
		return "main"
	case rtTableLocal:
		return "local"
	case rtTableDefault:
		return "default"
	default:
		return strconv.Itoa(t)
	}
}

func (o *Observer) Routes(ctx context.Context) (map[int][]netcfg.RouteState, error) {
	payload := make([]byte, sizeofRtMsg)
	payload[0] = syscall.AF_UNSPEC
	msgs, err := dump(syscall.RTM_GETROUTE, payload)
	if err != nil {
		return nil, err
	}
	links, err := o.linkNames(ctx)
	if err != nil {
		return nil, err
	}
	out := map[int][]netcfg.RouteState{}
	for _, m := range msgs {
		if m.Header.Type != syscall.RTM_NEWROUTE || len(m.Data) < sizeofRtMsg {
			continue
		}
		family := m.Data[0]
		dstLen := int(m.Data[1])
		table := int(m.Data[4])
		scope := m.Data[6]
		attrs, err := parseAttrs(m.Data[sizeofRtMsg:])
		if err != nil {
			return nil, err
		}
		r := netcfg.RouteState{Table: table, Scope: scopeName(scope)}
		for _, a := range attrs {
			switch a.Type {
			case rtaTable:
				if v, ok := attrU32(a); ok {
					r.Table = int(v)
				}
			case rtaDst:
				if addr, ok := attrAddr(a); ok {
					r.Dst = netip.PrefixFrom(addr, dstLen)
				}
			case rtaGateway:
				if addr, ok := attrAddr(a); ok {
					r.Gw = addr
				}
			case rtaPrefSrc:
				if addr, ok := attrAddr(a); ok {
					r.Src = addr
				}
			case rtaOif:
				if v, ok := attrU32(a); ok {
					r.Dev = links[int(v)]
				}
			}
		}
		if !r.Dst.IsValid() {
			zero := netip.IPv4Unspecified()
			if family == syscall.AF_INET6 {
				zero = netip.IPv6Unspecified()
			}
			r.Dst = netip.PrefixFrom(zero, 0)
		}
		out[r.Table] = append(out[r.Table], r)
	}
	return out, nil
}

func scopeName(s uint8) string {
	switch s {
	case 0:
		return "global"
	case 200:
		return "site"
	case 253:
		return "link"
	case 254:
		return "host"
	case 255:
		return "nowhere"
	default:
		return strconv.Itoa(int(s))
	}
}

func (o *Observer) linkNames(ctx context.Context) (map[int]string, error) {
	payload := make([]byte, sizeofIfInfomsg)
	payload[0] = syscall.AF_UNSPEC
	msgs, err := dump(syscall.RTM_GETLINK, payload)
	if err != nil {
		return nil, err
	}
	out := map[int]string{}
	for _, m := range msgs {
		if m.Header.Type != syscall.RTM_NEWLINK || len(m.Data) < sizeofIfInfomsg {
			continue
		}
		index := int(int32(binary.NativeEndian.Uint32(m.Data[4:8])))
		attrs, err := parseAttrs(m.Data[sizeofIfInfomsg:])
		if err != nil {
			return nil, err
		}
		for _, a := range attrs {
			if a.Type == iflaIfname {
				out[index] = attrString(a)
			}
		}
	}
	return out, nil
}

func (o *Observer) RpFilterAll() int {
	v, err := o.readIntSysctl("net/ipv4/conf/all/rp_filter")
	if err != nil {
		return -1
	}
	return v
}

func (o *Observer) IPForward() bool {
	v, err := o.readIntSysctl("net/ipv4/ip_forward")
	if err != nil {
		return false
	}
	return v != 0
}

func (o *Observer) readIntSysctl(rel string) (int, error) {
	root := o.ProcRoot
	if root == "" {
		root = "/proc"
	}
	raw, err := os.ReadFile(filepath.Join(root, "sys", rel))
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(raw)))
}

func DuplicateAddrs(links map[string]netcfg.LinkState) []netip.Prefix {
	owners := map[netip.Addr]map[string]struct{}{}
	for name, l := range links {
		for _, p := range l.Addrs {
			a := p.Addr()
			if a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() || a.IsMulticast() {
				continue
			}
			if owners[a] == nil {
				owners[a] = map[string]struct{}{}
			}
			owners[a][name] = struct{}{}
		}
	}
	var out []netip.Prefix
	for a, set := range owners {
		if len(set) > 1 {
			out = append(out, netip.PrefixFrom(a, a.BitLen()))
		}
	}
	sortPrefixes(out)
	return out
}

func sortPrefixes(p []netip.Prefix) {
	for i := 1; i < len(p); i++ {
		for j := i; j > 0 && p[j].Addr().Less(p[j-1].Addr()); j-- {
			p[j], p[j-1] = p[j-1], p[j]
		}
	}
}

type subscription struct {
	fd      int
	ch      chan netcfg.LinkEvent
	stopped atomic.Bool
}

func (o *Observer) Subscribe(ctx context.Context) (<-chan netcfg.LinkEvent, func(), error) {
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, syscall.NETLINK_ROUTE)
	if err != nil {
		return nil, nil, err
	}
	lsa := &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK, Groups: rtnlGroupLink}
	if err := syscall.Bind(fd, lsa); err != nil {
		syscall.Close(fd)
		return nil, nil, err
	}
	tv := syscall.Timeval{Sec: 0, Usec: 400000}
	if err := syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv); err != nil {
		syscall.Close(fd)
		return nil, nil, err
	}
	s := &subscription{fd: fd, ch: make(chan netcfg.LinkEvent, 64)}
	go func() {
		defer close(s.ch)
		defer syscall.Close(fd)
		seen := map[int]struct{}{}
		for {
			if s.stopped.Load() || ctx.Err() != nil {
				return
			}
			buf := make([]byte, 1<<16)
			n, _, err := syscall.Recvfrom(fd, buf, 0)
			if err != nil {
				if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK || err == syscall.EINTR {
					continue
				}
				return
			}
			msgs, err := syscall.ParseNetlinkMessage(buf[:n])
			if err != nil {
				continue
			}
			for _, m := range msgs {
				ev, ok := decodeLinkEvent(m, seen)
				if !ok {
					continue
				}
				select {
				case s.ch <- ev:
				default:
				}
			}
		}
	}()
	return s.ch, func() { s.stopped.Store(true) }, nil
}

func decodeLinkEvent(m syscall.NetlinkMessage, seen map[int]struct{}) (netcfg.LinkEvent, bool) {
	var kind netcfg.LinkEventKind
	switch m.Header.Type {
	case syscall.RTM_NEWLINK:
		kind = netcfg.LinkAdded
	case syscall.RTM_DELLINK:
		kind = netcfg.LinkDeleted
	default:
		return netcfg.LinkEvent{}, false
	}
	if len(m.Data) < sizeofIfInfomsg {
		return netcfg.LinkEvent{}, false
	}
	index := int(int32(binary.NativeEndian.Uint32(m.Data[4:8])))
	attrs, err := parseAttrs(m.Data[sizeofIfInfomsg:])
	if err != nil {
		return netcfg.LinkEvent{}, false
	}
	st := netcfg.LinkState{Index: index, OperState: netcfg.OperStateUnknown}
	for _, a := range attrs {
		switch a.Type {
		case iflaIfname:
			st.Name = attrString(a)
		case iflaAddress:
			st.MAC = attrMAC(a)
		case iflaMTU:
			if v, ok := attrU32(a); ok {
				st.MTU = int(v)
			}
		case iflaOperState:
			if len(a.Data) > 0 {
				if name, ok := operStateNames[a.Data[0]]; ok {
					st.OperState = name
				}
			}
		case iflaCarrier:
			if len(a.Data) > 0 {
				st.Carrier = a.Data[0] != 0
			}
		}
	}
	if st.Name == "" {
		return netcfg.LinkEvent{}, false
	}
	switch kind {
	case netcfg.LinkDeleted:
		delete(seen, index)
	case netcfg.LinkAdded:
		if _, ok := seen[index]; ok {
			kind = netcfg.LinkChanged
		}
		seen[index] = struct{}{}
	}
	return netcfg.LinkEvent{Kind: kind, Link: st, TS: time.Now().UnixMilli()}, true
}
