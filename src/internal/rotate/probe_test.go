package rotate

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"
)

func startEcho(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, host+"\n")
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func newTestProber(t *testing.T, echo ...string) *HTTPProber {
	t.Helper()
	p, err := NewProber(ProberOptions{Echo: echo, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("NewProber: %v", err)
	}
	return p
}

func TestSourceBoundProbeObservesTheAddressItBound(t *testing.T) {
	p := newTestProber(t, startEcho(t))

	got, err := p.ProbeSource(context.Background(), netip.MustParseAddr("127.0.0.2"))
	if err != nil {
		t.Fatalf("ProbeSource: %v", err)
	}
	if got.IP != netip.MustParseAddr("127.0.0.2") {
		t.Fatalf("probe observed %s, want the bound source 127.0.0.2", got.IP)
	}
	if got.Echo == "" {
		t.Fatal("probe did not record which echo endpoint answered")
	}
}

func TestAProbeThatLosesItsSourceRuleLooksLikeTheHostUplinkAndIsALeak(t *testing.T) {
	p := newTestProber(t, startEcho(t))
	hostUplink := netip.MustParseAddr("127.0.0.1")

	got, err := p.ProbeSource(context.Background(), netip.Addr{})
	if err != nil {
		t.Fatalf("ProbeSource without a source address: %v", err)
	}
	if got.IP != hostUplink {
		t.Fatalf("an unbound probe observed %s, want the host uplink %s", got.IP, hostUplink)
	}
	requireErrorIs(t, CheckLeak(got.IP, hostUplink), ErrProbeEgressLeak, "CheckLeak on an unbound probe")
}

func TestProbeFallsBackToTheNextEchoEndpoint(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer dead.Close()
	p := newTestProber(t, dead.URL, startEcho(t))

	got, err := p.ProbeSource(context.Background(), netip.MustParseAddr("127.0.0.4"))
	if err != nil {
		t.Fatalf("ProbeSource: %v", err)
	}
	if got.IP != netip.MustParseAddr("127.0.0.4") {
		t.Fatalf("probe observed %s after failing over, want 127.0.0.4", got.IP)
	}
}

func TestProbeSocksNegotiatesUserPassAndReportsTheUpstreamSource(t *testing.T) {
	echo := startEcho(t)
	addr := startSocks(t, socksOptions{User: "cust_01", Pass: "Kq7mZr2xTn9wLb4V", Source: net.ParseIP("127.0.0.5")})
	p := newTestProber(t, echo)

	got, err := p.ProbeSocks(context.Background(), Endpoint{
		Addr: netip.MustParseAddrPort(addr),
		User: "cust_01",
		Pass: "Kq7mZr2xTn9wLb4V",
	})
	if err != nil {
		t.Fatalf("ProbeSocks: %v", err)
	}
	if got.IP != netip.MustParseAddr("127.0.0.5") {
		t.Fatalf("socks probe observed %s, want the upstream source 127.0.0.5", got.IP)
	}
}

func TestProbeSocksReadsTheVerdictFromTheConnectReplyNotTheAuthReply(t *testing.T) {
	echo := startEcho(t)
	addr := startSocks(t, socksOptions{User: "cust_01", Pass: "right", Source: net.ParseIP("127.0.0.5")})
	p := newTestProber(t, echo)

	_, err := p.ProbeSocks(context.Background(), Endpoint{
		Addr: netip.MustParseAddrPort(addr),
		User: "cust_01",
		Pass: "wrong",
	})
	requireErrorIs(t, err, ErrProbeFailed, "ProbeSocks with a wrong password")
	if !strings.Contains(err.Error(), "denied") {
		t.Fatalf("ProbeSocks reported %v, want the connect reply verdict", err)
	}
}

func TestProbeSocksWithoutCredentials(t *testing.T) {
	echo := startEcho(t)
	addr := startSocks(t, socksOptions{Source: net.ParseIP("127.0.0.6")})
	p := newTestProber(t, echo)

	got, err := p.ProbeSocks(context.Background(), Endpoint{Addr: netip.MustParseAddrPort(addr)})
	if err != nil {
		t.Fatalf("ProbeSocks: %v", err)
	}
	if got.IP != netip.MustParseAddr("127.0.0.6") {
		t.Fatalf("socks probe observed %s, want 127.0.0.6", got.IP)
	}
}

func TestProbeHTTPUsesAnAbsoluteURIAndCarriesProxyAuthorization(t *testing.T) {
	seen := make(chan string, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.RequestURI, "http://") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		select {
		case seen <- r.Header.Get("Proxy-Authorization"):
		default:
		}
		io.WriteString(w, "203.0.113.7")
	}))
	defer proxy.Close()

	p := newTestProber(t, "http://example.invalid/ip")
	got, err := p.ProbeHTTP(context.Background(), Endpoint{
		Addr: netip.MustParseAddrPort(strings.TrimPrefix(proxy.URL, "http://")),
		User: "cust_01",
		Pass: "secret",
	})
	if err != nil {
		t.Fatalf("ProbeHTTP: %v", err)
	}
	if got.IP != netip.MustParseAddr("203.0.113.7") {
		t.Fatalf("http probe observed %s, want 203.0.113.7", got.IP)
	}
	auth := <-seen
	if auth != "Basic "+basicAuth("cust_01", "secret") {
		t.Fatalf("proxy saw authorization %q", auth)
	}
}

func TestProbeHTTPReportsAnAuthFailureWithoutSpendingEgress(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusProxyAuthRequired)
	}))
	defer proxy.Close()

	p := newTestProber(t, "http://example.invalid/ip")
	_, err := p.ProbeHTTP(context.Background(), Endpoint{
		Addr: netip.MustParseAddrPort(strings.TrimPrefix(proxy.URL, "http://")),
		User: "cust_01",
		Pass: "wrong",
	})
	requireErrorIs(t, err, ErrProbeFailed, "ProbeHTTP with a rejected password")
	if !strings.Contains(err.Error(), "407") {
		t.Fatalf("ProbeHTTP reported %v, want the 407 status", err)
	}
}

func TestParseEgressIPAcceptsTextAndJSON(t *testing.T) {
	cases := map[string]string{
		"203.0.113.7\n":                       "203.0.113.7",
		"  203.0.113.8  ":                     "203.0.113.8",
		`{"ip":"203.0.113.9","country":"KZ"}`: "203.0.113.9",
		"203.0.113.10\nsomething else":        "203.0.113.10",
		"2001:db8::1":                         "2001:db8::1",
	}
	for body, want := range cases {
		got, err := ParseEgressIP(body)
		if err != nil {
			t.Errorf("ParseEgressIP(%q): %v", body, err)
			continue
		}
		if got.String() != want {
			t.Errorf("ParseEgressIP(%q) = %s, want %s", body, got, want)
		}
	}
	for _, bad := range []string{"", "   ", "not an ip", "<html>nope</html>"} {
		if _, err := ParseEgressIP(bad); err == nil {
			t.Errorf("ParseEgressIP(%q) returned no error", bad)
		}
	}
}

func TestNewProberRejectsUnusableEndpoints(t *testing.T) {
	for _, bad := range []string{"ftp://example.com/ip", "://nope", "http:///nohost"} {
		if _, err := NewProber(ProberOptions{Echo: []string{bad}}); err == nil {
			t.Errorf("NewProber accepted %q", bad)
		}
	}
	if _, err := NewProber(ProberOptions{}); err != nil {
		t.Errorf("NewProber with defaults: %v", err)
	}
}

type socksOptions struct {
	User   string
	Pass   string
	Source net.IP
}

func startSocks(t *testing.T, o socksOptions) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serveSocks(c, o)
		}
	}()
	return ln.Addr().String()
}

func serveSocks(c net.Conn, o socksOptions) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))

	head := make([]byte, 2)
	if _, err := io.ReadFull(c, head); err != nil {
		return
	}
	methods := make([]byte, int(head[1]))
	if _, err := io.ReadFull(c, methods); err != nil {
		return
	}

	denied := false
	if o.User != "" {
		if _, err := c.Write([]byte{socksVersion, socksMethodUser}); err != nil {
			return
		}
		h := make([]byte, 2)
		if _, err := io.ReadFull(c, h); err != nil {
			return
		}
		user := make([]byte, int(h[1]))
		if _, err := io.ReadFull(c, user); err != nil {
			return
		}
		pl := make([]byte, 1)
		if _, err := io.ReadFull(c, pl); err != nil {
			return
		}
		pass := make([]byte, int(pl[0]))
		if _, err := io.ReadFull(c, pass); err != nil {
			return
		}
		if _, err := c.Write([]byte{socksAuthVer, 0x00}); err != nil {
			return
		}
		denied = string(user) != o.User || string(pass) != o.Pass
	} else if _, err := c.Write([]byte{socksVersion, socksMethodNone}); err != nil {
		return
	}

	req := make([]byte, 4)
	if _, err := io.ReadFull(c, req); err != nil {
		return
	}
	host := ""
	switch req[3] {
	case socksATypIPv4:
		b := make([]byte, 4)
		if _, err := io.ReadFull(c, b); err != nil {
			return
		}
		host = net.IP(b).String()
	case socksATypDomain:
		l := make([]byte, 1)
		if _, err := io.ReadFull(c, l); err != nil {
			return
		}
		b := make([]byte, int(l[0]))
		if _, err := io.ReadFull(c, b); err != nil {
			return
		}
		host = string(b)
	default:
		return
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(c, pb); err != nil {
		return
	}
	port := int(pb[0])<<8 | int(pb[1])

	if denied {
		c.Write([]byte{socksVersion, socksRepDenied, 0x00, socksATypIPv4, 0, 0, 0, 0, 0, 0})
		return
	}

	d := net.Dialer{Timeout: 5 * time.Second}
	if o.Source != nil {
		d.LocalAddr = &net.TCPAddr{IP: o.Source}
	}
	up, err := d.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		c.Write([]byte{socksVersion, 0x01, 0x00, socksATypIPv4, 0, 0, 0, 0, 0, 0})
		return
	}
	defer up.Close()
	if _, err := c.Write([]byte{socksVersion, socksRepOK, 0x00, socksATypIPv4, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	go io.Copy(up, c)
	io.Copy(c, up)
}

func requireErrorIs(t *testing.T, got, want error, what string) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s returned %v, want %v", what, got, want)
	}
}
