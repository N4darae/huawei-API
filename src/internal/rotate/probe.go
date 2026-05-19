package rotate

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultProbeTimeout = 10 * time.Second
	DefaultDialTimeout  = 5 * time.Second
	maxEchoBody         = 4096
)

const (
	socksVersion    = 0x05
	socksMethodNone = 0x00
	socksMethodUser = 0x02
	socksAuthVer    = 0x01
	socksCmdConnect = 0x01
	socksATypIPv4   = 0x01
	socksATypDomain = 0x03
	socksATypIPv6   = 0x04
	socksRepOK      = 0x00
	socksRepDenied  = 0x02
)

var (
	ErrProbeUnavailable = errors.New("rotate: no egress probe is configured")
	ErrProbeFailed      = errors.New("rotate: egress probe failed")
	ErrProbeNoEndpoint  = errors.New("rotate: no echo endpoint is configured")
	ErrProbeBadBody     = errors.New("rotate: echo endpoint did not answer with an ip address")
)

type Endpoint struct {
	Addr netip.AddrPort
	User string
	Pass string
}

func (e Endpoint) Authenticated() bool { return e.User != "" }

type EgressProbe struct {
	IP        netip.Addr
	LatencyMS int
	Echo      string
}

type Prober interface {
	ProbeSource(ctx context.Context, src netip.Addr) (EgressProbe, error)
	ProbeSocks(ctx context.Context, ep Endpoint) (EgressProbe, error)
	ProbeHTTP(ctx context.Context, ep Endpoint) (EgressProbe, error)
}

type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

type SourceDialFunc func(ctx context.Context, network, address string, src netip.Addr) (net.Conn, error)

type ProberOptions struct {
	Echo        []string
	Timeout     time.Duration
	DialTimeout time.Duration
	Dial        DialFunc
	DialFrom    SourceDialFunc
	UserAgent   string
}

type HTTPProber struct {
	echo      []echoTarget
	timeout   time.Duration
	dial      DialFunc
	dialFrom  SourceDialFunc
	userAgent string
}

var _ Prober = (*HTTPProber)(nil)

const DefaultUserAgent = "dongled-probe/1"

func DefaultEchoEndpoints() []string {
	return []string{
		"http://api.ipify.org/",
		"http://ifconfig.me/ip",
		"http://icanhazip.com/",
	}
}

func NewProber(o ProberOptions) (*HTTPProber, error) {
	if len(o.Echo) == 0 {
		o.Echo = DefaultEchoEndpoints()
	}
	targets := make([]echoTarget, 0, len(o.Echo))
	for _, raw := range o.Echo {
		t, err := parseEcho(raw)
		if err != nil {
			return nil, err
		}
		targets = append(targets, t)
	}
	if len(targets) == 0 {
		return nil, ErrProbeNoEndpoint
	}
	if o.Timeout <= 0 {
		o.Timeout = DefaultProbeTimeout
	}
	if o.DialTimeout <= 0 {
		o.DialTimeout = DefaultDialTimeout
	}
	if o.UserAgent == "" {
		o.UserAgent = DefaultUserAgent
	}
	p := &HTTPProber{echo: targets, timeout: o.Timeout, dial: o.Dial, dialFrom: o.DialFrom, userAgent: o.UserAgent}
	if p.dial == nil {
		p.dial = plainDialer(o.DialTimeout)
	}
	if p.dialFrom == nil {
		p.dialFrom = sourceDialer(o.DialTimeout)
	}
	return p, nil
}

type echoTarget struct {
	raw    string
	scheme string
	host   string
	port   int
	path   string
}

func (t echoTarget) address() string { return net.JoinHostPort(t.host, strconv.Itoa(t.port)) }

func (t echoTarget) tls() bool { return t.scheme == "https" }

func parseEcho(raw string) (echoTarget, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return echoTarget{}, fmt.Errorf("%w: %q: %v", ErrProbeNoEndpoint, raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return echoTarget{}, fmt.Errorf("%w: %q must be http or https", ErrProbeNoEndpoint, raw)
	}
	host := u.Hostname()
	if host == "" {
		return echoTarget{}, fmt.Errorf("%w: %q has no host", ErrProbeNoEndpoint, raw)
	}
	port := 80
	if u.Scheme == "https" {
		port = 443
	}
	if s := u.Port(); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 65535 {
			return echoTarget{}, fmt.Errorf("%w: %q has an invalid port", ErrProbeNoEndpoint, raw)
		}
		port = n
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	return echoTarget{raw: raw, scheme: u.Scheme, host: host, port: port, path: path}, nil
}

func plainDialer(timeout time.Duration) DialFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		d := net.Dialer{Timeout: timeout}
		return d.DialContext(ctx, network, address)
	}
}

func sourceDialer(timeout time.Duration) SourceDialFunc {
	return func(ctx context.Context, network, address string, src netip.Addr) (net.Conn, error) {
		d := net.Dialer{Timeout: timeout}
		if src.IsValid() {
			d.LocalAddr = &net.TCPAddr{IP: net.IP(src.AsSlice())}
		}
		return d.DialContext(ctx, network, address)
	}
}

func (p *HTTPProber) ProbeSource(ctx context.Context, src netip.Addr) (EgressProbe, error) {
	network := "tcp"
	if src.Is4() {
		network = "tcp4"
	}
	return p.attempt(ctx, func(ctx context.Context, t echoTarget) (net.Conn, error) {
		return p.dialFrom(ctx, network, t.address(), src)
	}, func(ctx context.Context, c net.Conn, t echoTarget) (netip.Addr, error) {
		return readEcho(ctx, c, t, p.userAgent, t.path, "")
	})
}

func (p *HTTPProber) ProbeSocks(ctx context.Context, ep Endpoint) (EgressProbe, error) {
	if !ep.Addr.IsValid() {
		return EgressProbe{}, fmt.Errorf("%w: socks endpoint is not set", ErrProbeFailed)
	}
	return p.attempt(ctx, func(ctx context.Context, t echoTarget) (net.Conn, error) {
		return p.dial(ctx, "tcp", ep.Addr.String())
	}, func(ctx context.Context, c net.Conn, t echoTarget) (netip.Addr, error) {
		if err := socksConnect(ctx, c, ep, t); err != nil {
			return netip.Addr{}, err
		}
		return readEcho(ctx, c, t, p.userAgent, t.path, "")
	})
}

func (p *HTTPProber) ProbeHTTP(ctx context.Context, ep Endpoint) (EgressProbe, error) {
	if !ep.Addr.IsValid() {
		return EgressProbe{}, fmt.Errorf("%w: http endpoint is not set", ErrProbeFailed)
	}
	return p.attempt(ctx, func(ctx context.Context, t echoTarget) (net.Conn, error) {
		return p.dial(ctx, "tcp", ep.Addr.String())
	}, func(ctx context.Context, c net.Conn, t echoTarget) (netip.Addr, error) {
		if t.tls() {
			if err := httpProxyTunnel(ctx, c, ep, t); err != nil {
				return netip.Addr{}, err
			}
			return readEcho(ctx, c, t, p.userAgent, t.path, "")
		}
		auth := ""
		if ep.Authenticated() {
			auth = basicAuth(ep.User, ep.Pass)
		}
		absolute := t.scheme + "://" + t.address() + t.path
		return readEchoPlain(ctx, c, t, p.userAgent, absolute, auth)
	})
}

type dialStep func(ctx context.Context, t echoTarget) (net.Conn, error)

type readStep func(ctx context.Context, c net.Conn, t echoTarget) (netip.Addr, error)

func (p *HTTPProber) attempt(ctx context.Context, dial dialStep, read readStep) (EgressProbe, error) {
	var last error
	for _, t := range p.echo {
		select {
		case <-ctx.Done():
			return EgressProbe{}, ctx.Err()
		default:
		}
		one, cancel := context.WithTimeout(ctx, p.timeout)
		start := time.Now()
		ip, err := p.once(one, t, dial, read)
		elapsed := time.Since(start)
		cancel()
		if err != nil {
			last = err
			continue
		}
		return EgressProbe{IP: ip, LatencyMS: int(elapsed / time.Millisecond), Echo: t.raw}, nil
	}
	if last == nil {
		last = ErrProbeNoEndpoint
	}
	return EgressProbe{}, last
}

func (p *HTTPProber) once(ctx context.Context, t echoTarget, dial dialStep, read readStep) (netip.Addr, error) {
	c, err := dial(ctx, t)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("%w: dial %s: %v", ErrProbeFailed, t.address(), err)
	}
	defer c.Close()
	setDeadline(ctx, c)
	return read(ctx, c, t)
}

func setDeadline(ctx context.Context, c net.Conn) {
	if d, ok := ctx.Deadline(); ok {
		c.SetDeadline(d)
		return
	}
	c.SetDeadline(time.Now().Add(DefaultProbeTimeout))
}

func readEcho(ctx context.Context, c net.Conn, t echoTarget, agent, target, auth string) (netip.Addr, error) {
	if t.tls() {
		tc := tls.Client(c, &tls.Config{ServerName: t.host})
		if err := tc.HandshakeContext(ctx); err != nil {
			return netip.Addr{}, fmt.Errorf("%w: tls %s: %v", ErrProbeFailed, t.host, err)
		}
		return readEchoPlain(ctx, tc, t, agent, target, auth)
	}
	return readEchoPlain(ctx, c, t, agent, target, auth)
}

func readEchoPlain(_ context.Context, c net.Conn, t echoTarget, agent, target, auth string) (netip.Addr, error) {
	var b strings.Builder
	b.WriteString("GET " + target + " HTTP/1.1\r\n")
	b.WriteString("Host: " + t.host + "\r\n")
	b.WriteString("User-Agent: " + agent + "\r\n")
	b.WriteString("Accept: text/plain\r\n")
	if auth != "" {
		b.WriteString("Proxy-Authorization: Basic " + auth + "\r\n")
	}
	b.WriteString("Connection: close\r\n\r\n")
	if _, err := io.WriteString(c, b.String()); err != nil {
		return netip.Addr{}, fmt.Errorf("%w: write %s: %v", ErrProbeFailed, t.raw, err)
	}

	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("%w: read %s: %v", ErrProbeFailed, t.raw, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return netip.Addr{}, fmt.Errorf("%w: %s answered %d", ErrProbeFailed, t.raw, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxEchoBody))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("%w: body %s: %v", ErrProbeFailed, t.raw, err)
	}
	return ParseEgressIP(string(body))
}

func ParseEgressIP(body string) (netip.Addr, error) {
	s := strings.TrimSpace(body)
	if s == "" {
		return netip.Addr{}, fmt.Errorf("%w: empty body", ErrProbeBadBody)
	}
	if strings.HasPrefix(s, "{") {
		if v, ok := jsonField(s, "ip"); ok {
			s = v
		}
	}
	s = strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
	s = strings.Trim(s, `"' `)
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("%w: %q", ErrProbeBadBody, truncate(s, 64))
	}
	return a.Unmap(), nil
}

func jsonField(s, name string) (string, bool) {
	key := `"` + name + `"`
	i := strings.Index(s, key)
	if i < 0 {
		return "", false
	}
	rest := s[i+len(key):]
	j := strings.Index(rest, ":")
	if j < 0 {
		return "", false
	}
	rest = strings.TrimSpace(rest[j+1:])
	if !strings.HasPrefix(rest, `"`) {
		return "", false
	}
	rest = rest[1:]
	k := strings.Index(rest, `"`)
	if k < 0 {
		return "", false
	}
	return rest[:k], true
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func basicAuth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}

func socksConnect(_ context.Context, c net.Conn, ep Endpoint, t echoTarget) error {
	methods := []byte{socksVersion, 1, socksMethodNone}
	if ep.Authenticated() {
		methods = []byte{socksVersion, 2, socksMethodNone, socksMethodUser}
	}
	if _, err := c.Write(methods); err != nil {
		return fmt.Errorf("%w: socks greeting: %v", ErrProbeFailed, err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(c, reply); err != nil {
		return fmt.Errorf("%w: socks greeting reply: %v", ErrProbeFailed, err)
	}
	if reply[0] != socksVersion {
		return fmt.Errorf("%w: %s is not a socks5 listener (first byte %#x)", ErrProbeFailed, ep.Addr, reply[0])
	}
	if reply[1] == socksMethodUser {
		if !ep.Authenticated() {
			return fmt.Errorf("%w: %s demands credentials the proxy record does not carry", ErrProbeFailed, ep.Addr)
		}
		auth := []byte{socksAuthVer, byte(len(ep.User))}
		auth = append(auth, ep.User...)
		auth = append(auth, byte(len(ep.Pass)))
		auth = append(auth, ep.Pass...)
		if _, err := c.Write(auth); err != nil {
			return fmt.Errorf("%w: socks auth: %v", ErrProbeFailed, err)
		}
		if _, err := io.ReadFull(c, reply); err != nil {
			return fmt.Errorf("%w: socks auth reply: %v", ErrProbeFailed, err)
		}
	} else if reply[1] != socksMethodNone {
		return fmt.Errorf("%w: %s selected auth method %#x", ErrProbeFailed, ep.Addr, reply[1])
	}

	req := []byte{socksVersion, socksCmdConnect, 0x00}
	if a, err := netip.ParseAddr(t.host); err == nil && a.Is4() {
		v4 := a.As4()
		req = append(req, socksATypIPv4)
		req = append(req, v4[:]...)
	} else {
		if len(t.host) > 255 {
			return fmt.Errorf("%w: echo host is too long for socks5", ErrProbeFailed)
		}
		req = append(req, socksATypDomain, byte(len(t.host)))
		req = append(req, t.host...)
	}
	req = append(req, byte(t.port>>8), byte(t.port))
	if _, err := c.Write(req); err != nil {
		return fmt.Errorf("%w: socks connect: %v", ErrProbeFailed, err)
	}

	head := make([]byte, 4)
	if _, err := io.ReadFull(c, head); err != nil {
		return fmt.Errorf("%w: socks connect reply: %v", ErrProbeFailed, err)
	}
	if head[1] == socksRepDenied {
		return fmt.Errorf("%w: socks %s denied user %q", ErrProbeFailed, ep.Addr, ep.User)
	}
	if head[1] != socksRepOK {
		return fmt.Errorf("%w: socks %s replied %#x", ErrProbeFailed, ep.Addr, head[1])
	}
	return drainSocksBound(c, head[3])
}

func drainSocksBound(c net.Conn, atyp byte) error {
	var n int
	switch atyp {
	case socksATypIPv4:
		n = 4
	case socksATypIPv6:
		n = 16
	case socksATypDomain:
		l := make([]byte, 1)
		if _, err := io.ReadFull(c, l); err != nil {
			return fmt.Errorf("%w: socks bound length: %v", ErrProbeFailed, err)
		}
		n = int(l[0])
	default:
		return fmt.Errorf("%w: socks bound address type %#x", ErrProbeFailed, atyp)
	}
	if _, err := io.ReadFull(c, make([]byte, n+2)); err != nil {
		return fmt.Errorf("%w: socks bound address: %v", ErrProbeFailed, err)
	}
	return nil
}

func httpProxyTunnel(_ context.Context, c net.Conn, ep Endpoint, t echoTarget) error {
	var b strings.Builder
	b.WriteString("CONNECT " + t.address() + " HTTP/1.1\r\n")
	b.WriteString("Host: " + t.address() + "\r\n")
	if ep.Authenticated() {
		b.WriteString("Proxy-Authorization: Basic " + basicAuth(ep.User, ep.Pass) + "\r\n")
	}
	b.WriteString("\r\n")
	if _, err := io.WriteString(c, b.String()); err != nil {
		return fmt.Errorf("%w: connect %s: %v", ErrProbeFailed, ep.Addr, err)
	}
	line, err := readLineRaw(c)
	if err != nil {
		return fmt.Errorf("%w: connect reply %s: %v", ErrProbeFailed, ep.Addr, err)
	}
	parts := strings.Fields(strings.TrimSpace(line))
	if len(parts) < 2 {
		return fmt.Errorf("%w: connect reply %q", ErrProbeFailed, truncate(line, 64))
	}
	code, err := strconv.Atoi(parts[1])
	if err != nil || code != http.StatusOK {
		return fmt.Errorf("%w: http proxy %s answered %q to CONNECT", ErrProbeFailed, ep.Addr, truncate(strings.TrimSpace(line), 64))
	}
	for {
		l, err := readLineRaw(c)
		if err != nil {
			return fmt.Errorf("%w: connect headers %s: %v", ErrProbeFailed, ep.Addr, err)
		}
		if strings.TrimSpace(l) == "" {
			return nil
		}
	}
}

func readLineRaw(c net.Conn) (string, error) {
	var b strings.Builder
	one := make([]byte, 1)
	for b.Len() < maxEchoBody {
		if _, err := io.ReadFull(c, one); err != nil {
			return b.String(), err
		}
		b.WriteByte(one[0])
		if one[0] == '\n' {
			return b.String(), nil
		}
	}
	return b.String(), fmt.Errorf("%w: header line is too long", ErrProbeFailed)
}
