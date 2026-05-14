package proxysup

import (
	"errors"
	"net/netip"
	"os"
	"strings"
	"testing"
)

func TestParseLogLineFromRealBinaryOutput(t *testing.T) {
	raw, err := os.ReadFile("testdata/px01.log")
	if err != nil {
		t.Fatal(err)
	}
	lines := 0
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		lines++
		r, err := ParseLogLine(l)
		if err != nil {
			t.Fatalf("line %q: %v", l, err)
		}
		if r.At.IsZero() {
			t.Fatalf("line %q: no timestamp", l)
		}
		if r.Service == "" || r.ServicePort == 0 {
			t.Fatalf("line %q: service %q port %d", l, r.Service, r.ServicePort)
		}
		if !r.External.IsValid() {
			t.Fatalf("line %q: %%e did not parse", l)
		}
	}
	if lines < 10 {
		t.Fatalf("fixture is too small: %d lines", lines)
	}
}

func TestParseLogLineFields(t *testing.T) {
	line := "08-08-2026 12:20:12 +0000 SOCK5.22952 00005 u1 127.0.0.1:38568 127.0.0.1:20999 12 34 0 CONNECT 127.0.0.1:20999 192.168.101.100"
	r, err := ParseLogLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if r.Service != "SOCK5" || r.ServicePort != 22952 {
		t.Fatalf("service %q port %d", r.Service, r.ServicePort)
	}
	if r.ErrorCode != "00005" || !r.AuthFailed() {
		t.Fatalf("error code %q", r.ErrorCode)
	}
	if r.User != "u1" {
		t.Fatalf("user %q", r.User)
	}
	if r.Client != netip.MustParseAddrPort("127.0.0.1:38568") {
		t.Fatalf("client %v", r.Client)
	}
	if r.Remote != netip.MustParseAddrPort("127.0.0.1:20999") {
		t.Fatalf("remote %v", r.Remote)
	}
	if r.BytesOut != 12 || r.BytesIn != 34 {
		t.Fatalf("bytes out %d in %d", r.BytesOut, r.BytesIn)
	}
	if r.Text != "CONNECT 127.0.0.1:20999" {
		t.Fatalf("text %q", r.Text)
	}
	if r.External != netip.MustParseAddr("192.168.101.100") {
		t.Fatalf("external %v", r.External)
	}
}

func TestParseLogLineDetectsBindFailure(t *testing.T) {
	line := "08-08-2026 12:20:12 +0000 PROXY.22001 00012 - 0.0.0.0:0 0.0.0.0:0 0 0 0 bind 0.0.0.0"
	r, err := ParseLogLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if !r.BindFailed() {
		t.Fatalf("00012 must read as a bind failure: %+v", r)
	}
}

func TestParseLogLineRejectsShortLines(t *testing.T) {
	if _, err := ParseLogLine("08-08-2026 12:20:12 +0000 PROXY.22001"); !errors.Is(err, ErrLogParse) {
		t.Fatalf("err = %v, want ErrLogParse", err)
	}
}

func TestLogFormatIsFrozenAndCarriesExternal(t *testing.T) {
	if !strings.Contains(LogFormat, "%e") {
		t.Fatal("logformat must carry the external address verb, the cheapest stale-external signal")
	}
	if !strings.HasPrefix(LogFormat, `"L`) {
		t.Fatalf("logformat must start with L for local time: %s", LogFormat)
	}
}
