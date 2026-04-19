package proxysup

import (
	"bytes"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

type Line struct {
	Num    int
	Raw    string
	Fields []string
	Quoted []bool
}

func (l Line) Cmd() string {
	if len(l.Fields) == 0 {
		return ""
	}
	return l.Fields[0]
}

func (l Line) Arg(i int) string {
	if i+1 >= len(l.Fields) {
		return ""
	}
	return l.Fields[i+1]
}

func (l Line) Args() []string {
	if len(l.Fields) < 2 {
		return nil
	}
	return l.Fields[1:]
}

func Render(sp Spec) ([]byte, error) {
	if err := sp.Validate(); err != nil {
		return nil, err
	}

	var b bytes.Buffer
	w := func(format string, args ...any) {
		fmt.Fprintf(&b, format+"\n", args...)
	}

	w("nscache %d", NsCacheSize)
	for _, ns := range sp.NServers {
		w("nserver %s", ns)
	}
	w("timeouts %s", Timeouts)
	w("noforce")
	w("log %s D", sp.LogPath)
	w("rotate %d", LogRotateDays)
	w("logformat %s", LogFormat)
	w("logdump %d %d", LogDumpBytes, LogDumpBytes)
	w("setgid %d", sp.Slot.GID())
	w("setuid %d", sp.Slot.UID())
	w("maxconn %d", sp.Policy.MaxConn)
	if sp.Policy.ConnLimit > 0 {
		w("connlim %d 0 * *", sp.Policy.ConnLimit)
	}
	w("auth %s", strings.Join(authTypes(sp.AuthMode), " "))
	for _, u := range sp.Users {
		w("users %q", u.Name+":"+PasswordType+":"+u.Password)
	}
	w("external %s", sp.ExternalIP)
	w("internal %s", sp.InternalIP)

	writeACL(w, sp)
	w("%s -p%d %s %s", ServiceProxy, sp.HTTPPort, AnonFlag, FamilyFlag)
	writeACL(w, sp)
	w("%s -p%d %s %s", ServiceSocks, sp.SocksPort, AnonFlag, FamilyFlag)

	out := b.Bytes()
	if err := Verify(out, sp); err != nil {
		return nil, err
	}
	return out, nil
}

func writeACL(w func(string, ...any), sp Spec) {
	w("flush")
	ports := portList(sp.Policy)
	if sp.AuthMode.UsesUserPass() {
		for _, u := range sp.Users {
			if ports == "" {
				w("allow %s", u.Name)
			} else {
				w("allow %s * * %s", u.Name, ports)
			}
		}
	}
	if sp.AuthMode.UsesIPList() {
		for _, p := range sortedPrefixes(sp.AuthIPs) {
			if ports == "" {
				w("allow * %s", p)
			} else {
				w("allow * %s * %s", p, ports)
			}
		}
	}
	w("deny *")
}

func authTypes(m domain.AuthMode) []string {
	if m.UsesIPList() {
		return []string{AuthIPOnly, AuthStrong}
	}
	return []string{AuthStrong}
}

func portList(p domain.ProxyPolicy) string {
	if p.AllowAllPorts || len(p.AllowedPorts) == 0 {
		return ""
	}
	return domain.FormatPortRanges(p.AllowedPorts)
}

func sortedPrefixes(in []netip.Prefix) []netip.Prefix {
	out := append([]netip.Prefix(nil), in...)
	domain.SortPrefixes(out)
	return out
}

func Verify(cfg []byte, sp Spec) error {
	lines := Parse(cfg)

	if err := verifyGlobals(lines, sp); err != nil {
		return err
	}
	if err := verifyUsers(lines, sp); err != nil {
		return err
	}
	if err := verifyACLBlocks(lines, sp); err != nil {
		return err
	}
	return verifyServices(lines, sp)
}

func verifyGlobals(lines []Line, sp Spec) error {
	var (
		sawAuthStrong bool
		sawNoforce    bool
		sawInternal   bool
		sawLogFormat  bool
		sawTimeouts   bool
		sawNServer    bool
	)
	for _, l := range lines {
		switch l.Cmd() {
		case "auth":
			types := map[string]bool{}
			for _, t := range l.Args() {
				types[t] = true
			}
			if types[AuthNone] {
				return fmt.Errorf("%w: line %d disables authentication", ErrNoAuthStrong, l.Num)
			}
			if sp.AuthMode.UsesIPList() && !types[AuthIPOnly] {
				return fmt.Errorf("%w: line %d has no %s for auth mode %q", ErrNoAuthIPs, l.Num, AuthIPOnly, string(sp.AuthMode))
			}
			if !sp.AuthMode.UsesIPList() && types[AuthIPOnly] {
				return fmt.Errorf("%w: line %d allows %s for auth mode %q", ErrNoAuthStrong, l.Num, AuthIPOnly, string(sp.AuthMode))
			}
			sawAuthStrong = types[AuthStrong]
		case "noforce":
			sawNoforce = true
		case "internal":
			a, err := netip.ParseAddr(l.Arg(0))
			if err != nil || a != sp.InternalIP {
				return fmt.Errorf("%w: line %d binds %q, spec wants %s", ErrNoInternal, l.Num, l.Arg(0), sp.InternalIP)
			}
			sawInternal = true
		case "nserver":
			sawNServer = true
		case "logformat":
			if !strings.Contains(l.Arg(0), "%e") {
				return fmt.Errorf("%w: line %d", ErrNoLogFormat, l.Num)
			}
			sawLogFormat = true
		case "timeouts":
			if n := len(l.Args()); n != TimeoutsCount {
				return fmt.Errorf("%w: line %d carries %d values", ErrBadTimeouts, l.Num, n)
			}
			sawTimeouts = true
		case "setuid":
			if l.Arg(0) != strconv.Itoa(sp.Slot.UID()) {
				return fmt.Errorf("%w: line %d sets uid %q, slot %s owns %d", ErrUIDMismatch, l.Num, l.Arg(0), sp.Slot, sp.Slot.UID())
			}
		case "setgid":
			if l.Arg(0) != strconv.Itoa(sp.Slot.GID()) {
				return fmt.Errorf("%w: line %d sets gid %q, slot %s owns %d", ErrUIDMismatch, l.Num, l.Arg(0), sp.Slot, sp.Slot.GID())
			}
		case "daemon":
			return fmt.Errorf("%w: line %d must not daemonize under systemd", domain.ErrInvalid, l.Num)
		}
	}
	switch {
	case !sawAuthStrong:
		return ErrNoAuthStrong
	case !sawNoforce:
		return ErrNoNoforce
	case !sawInternal:
		return ErrNoInternal
	case !sawLogFormat:
		return ErrNoLogFormat
	case !sawTimeouts:
		return ErrBadTimeouts
	case !sawNServer:
		return ErrNoNServer
	}
	return nil
}

func verifyUsers(lines []Line, sp Spec) error {
	got := map[string]string{}
	for _, l := range lines {
		if l.Cmd() != "users" {
			continue
		}
		for i, f := range l.Args() {
			if !l.Quoted[i+1] {
				return fmt.Errorf("%w: line %d field %q", ErrUnquotedUser, l.Num, f)
			}
			name, typ, pass, err := splitUserEntry(f)
			if err != nil {
				return fmt.Errorf("%w: line %d: %v", ErrUnquotedUser, l.Num, err)
			}
			if typ != PasswordType {
				return fmt.Errorf("%w: line %d uses password type %q", domain.ErrInvalid, l.Num, typ)
			}
			if !ValidUsername(name) {
				return fmt.Errorf("%w: line %d", ErrBadUsername, l.Num)
			}
			if !ValidPassword(pass) {
				return fmt.Errorf("%w: line %d user %q", ErrBadPassword, l.Num, name)
			}
			got[name] = pass
		}
	}
	if sp.AuthMode.UsesUserPass() && len(got) == 0 {
		return ErrNoUsers
	}
	if !sp.AuthMode.UsesUserPass() && len(got) > 0 {
		return ErrUsersUnused
	}
	for _, u := range sp.Users {
		if got[u.Name] != u.Password {
			return fmt.Errorf("%w: user %q is not in the rendered config", ErrNoUsers, u.Name)
		}
	}
	return nil
}

func splitUserEntry(s string) (name, typ, pass string, err error) {
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf("entry %q is not name:type:password", s)
	}
	return parts[0], parts[1], parts[2], nil
}

func verifyACLBlocks(lines []Line, sp Spec) error {
	var (
		sawFlush   bool
		sawDenyAll bool
		users      = map[string]bool{}
		nets       = map[string]bool{}
		blocks     int
	)
	for _, l := range lines {
		switch l.Cmd() {
		case "flush":
			sawFlush = true
			sawDenyAll = false
			users = map[string]bool{}
			nets = map[string]bool{}
		case "allow", "deny", "redirect", "connlim", "noconnlim",
			"bandlimin", "bandlimout", "nobandlimin", "nobandlimout",
			"countin", "countout", "countall", "nocountin", "nocountout", "nocountall":
			if err := verifyACLOperations(l); err != nil {
				return err
			}
			if l.Cmd() == "deny" && l.Arg(0) == "*" && len(l.Args()) == 1 {
				sawDenyAll = true
			}
			if l.Cmd() == "allow" && sawDenyAll {
				return fmt.Errorf("%w: line %d follows a trailing deny *", ErrNoDenyAll, l.Num)
			}
			if l.Cmd() == "allow" {
				if l.Arg(0) == "*" {
					nets[l.Arg(1)] = true
				} else {
					users[l.Arg(0)] = true
				}
			}
		case ServiceProxy, ServiceSocks:
			blocks++
			if !sawFlush {
				return fmt.Errorf("%w: service on line %d", ErrNoFlush, l.Num)
			}
			if !sawDenyAll {
				return fmt.Errorf("%w: service on line %d", ErrNoDenyAll, l.Num)
			}
			if sp.AuthMode.UsesUserPass() {
				for _, u := range sp.Users {
					if !users[u.Name] {
						return fmt.Errorf("%w: user %q is not allowed before the service on line %d", ErrNoUsers, u.Name, l.Num)
					}
				}
			}
			if sp.AuthMode.UsesIPList() {
				if len(nets) == 0 {
					return fmt.Errorf("%w: service on line %d", ErrNoAuthIPs, l.Num)
				}
				for _, p := range sp.AuthIPs {
					if !nets[p.String()] {
						return fmt.Errorf("%w: %s is not allowed before the service on line %d", ErrNoAuthIPs, p, l.Num)
					}
				}
			}
			sawFlush = false
		}
	}
	if blocks != 2 {
		return fmt.Errorf("%w: expected exactly 2 services, got %d", domain.ErrInvalid, blocks)
	}
	return nil
}

func verifyACLOperations(l Line) error {
	idx := aclOperationIndex(l.Cmd())
	if idx < 0 {
		return nil
	}
	ops := l.Arg(idx)
	if ops == "" || ops == "*" {
		return nil
	}
	valid := map[string]bool{}
	for _, v := range ValidOperations() {
		valid[v] = true
	}
	for _, op := range strings.Split(ops, ",") {
		op = strings.TrimSpace(op)
		if op == "" || valid[op] {
			continue
		}
		return fmt.Errorf("%w: line %d uses %q", ErrBadOperation, l.Num, op)
	}
	return nil
}

func aclOperationIndex(cmd string) int {
	switch cmd {
	case "allow", "deny":
		return 4
	case "redirect":
		return 6
	case "connlim", "bandlimin", "bandlimout":
		return 6
	case "countin", "countout", "countall":
		return 8
	case "noconnlim", "nobandlimin", "nobandlimout", "nocountin", "nocountout", "nocountall":
		return 4
	}
	return -1
}

func verifyServices(lines []Line, sp Spec) error {
	want := map[string]int{ServiceProxy: sp.HTTPPort, ServiceSocks: sp.SocksPort}
	seen := map[string]bool{}
	for _, l := range lines {
		cmd := l.Cmd()
		port, ok := want[cmd]
		if !ok {
			continue
		}
		var sawPort, sawAnon, sawFamily bool
		for _, a := range l.Args() {
			switch {
			case a == AnonFlag:
				sawAnon = true
			case strings.HasPrefix(a, "-a"):
				return fmt.Errorf("%w: line %d uses %q", ErrBadAnonFlag, l.Num, a)
			case a == FamilyFlag:
				sawFamily = true
			case strings.HasPrefix(a, "-p"):
				n, err := strconv.Atoi(strings.TrimPrefix(a, "-p"))
				if err != nil || n != port {
					return fmt.Errorf("%w: line %d listens on %q, spec wants %d", ErrPortMismatch, l.Num, a, port)
				}
				sawPort = true
			}
		}
		if !sawAnon {
			return fmt.Errorf("%w: line %d has no %s", ErrBadAnonFlag, l.Num, AnonFlag)
		}
		if !sawPort {
			return fmt.Errorf("%w: line %d has no -p", ErrPortMismatch, l.Num)
		}
		if !sawFamily {
			return fmt.Errorf("%w: line %d has no %s", domain.ErrInvalid, l.Num, FamilyFlag)
		}
		seen[cmd] = true
	}
	missing := make([]string, 0, 2)
	for k := range want {
		if !seen[k] {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("%w: missing services %s", domain.ErrInvalid, strings.Join(missing, ","))
	}
	return nil
}

func Parse(cfg []byte) []Line {
	out := []Line{}
	for i, raw := range strings.Split(string(cfg), "\n") {
		trimmed := strings.TrimRight(raw, "\r")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, " ") || strings.HasPrefix(trimmed, "\t") {
			continue
		}
		fields, quoted := tokenize(trimmed)
		if len(fields) == 0 {
			continue
		}
		out = append(out, Line{Num: i + 1, Raw: trimmed, Fields: fields, Quoted: quoted})
	}
	return out
}

func tokenize(s string) ([]string, []bool) {
	var (
		fields []string
		quoted []bool
		cur    strings.Builder
		inTok  bool
		inQ    bool
		wasQ   bool
	)
	flush := func() {
		if inTok {
			fields = append(fields, cur.String())
			quoted = append(quoted, wasQ)
			cur.Reset()
			inTok = false
			wasQ = false
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			inQ = !inQ
			inTok = true
			wasQ = true
		case (c == ' ' || c == '\t') && !inQ:
			flush()
		default:
			inTok = true
			cur.WriteByte(c)
		}
	}
	flush()
	return fields, quoted
}

func ConfigUsers(cfg []byte) map[string]string {
	out := map[string]string{}
	for _, l := range Parse(cfg) {
		if l.Cmd() != "users" {
			continue
		}
		for _, f := range l.Args() {
			name, _, pass, err := splitUserEntry(f)
			if err != nil {
				continue
			}
			out[name] = pass
		}
	}
	return out
}

func ConfigAuthNets(cfg []byte) map[string]bool {
	out := map[string]bool{}
	for _, l := range Parse(cfg) {
		if l.Cmd() != "allow" || l.Arg(0) != "*" || l.Arg(1) == "" {
			continue
		}
		out[l.Arg(1)] = true
	}
	return out
}

func RevokesAccess(oldCfg, newCfg []byte) bool {
	oldUsers, newUsers := ConfigUsers(oldCfg), ConfigUsers(newCfg)
	for name, pass := range oldUsers {
		if newUsers[name] != pass {
			return true
		}
	}
	oldNets, newNets := ConfigAuthNets(oldCfg), ConfigAuthNets(newCfg)
	for n := range oldNets {
		if !newNets[n] {
			return true
		}
	}
	return false
}
