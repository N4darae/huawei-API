package hilink

import (
	"net/netip"
	"strings"
	"testing"
)

func TestSuffixInt(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"-89dBm", -89},
		{"-102dBm", -102},
		{"-6dB", -6},
		{"3dB", 3},
		{"-15.0dB", -15},
		{"-9.5dB", -10},
		{"15MHz", 15},
		{"", 0},
		{"dBm", 0},
		{"-", 0},
		{"42", 42},
	}
	for _, c := range cases {
		if got := suffixInt(c.in); got != c.want {
			t.Errorf("suffixInt(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestMarshalRequestKeepsFieldOrder(t *testing.T) {
	req := dhcpRequest{
		DHCPIPAddress:      "192.168.101.1",
		DHCPLanNetmask:     "255.255.255.0",
		DHCPStatus:         "1",
		DHCPStartIPAddress: "192.168.101.100",
		DHCPEndIPAddress:   "192.168.101.200",
		DHCPLeaseTime:      "86400",
		DNSStatus:          "1",
		PrimaryDNS:         "192.168.101.1",
		SecondaryDNS:       "192.168.101.1",
		ShowDNSSetting:     "1",
	}
	b, err := MarshalRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.HasPrefix(got, XMLProlog+"<request>") {
		t.Fatalf("missing prolog or request root: %q", got)
	}
	want := []string{
		"DhcpIPAddress", "DhcpLanNetmask", "DhcpStatus", "DhcpStartIPAddress",
		"DhcpEndIPAddress", "DhcpLeaseTime", "DnsStatus", "PrimaryDns",
		"SecondaryDns", "ShowDnsSetting",
	}
	at := 0
	for _, name := range want {
		i := strings.Index(got[at:], "<"+name+">")
		if i < 0 {
			t.Fatalf("element %s missing or out of order in %q", name, got)
		}
		at += i
	}
}

func TestMarshalRequestKeepsMaxIdelTimeMisspelling(t *testing.T) {
	b, err := MarshalRequest(connectionRequest{MaxIdelTime: "300", MTU: "1500"})
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.Contains(got, "<MaxIdelTime>300</MaxIdelTime>") {
		t.Fatalf("MaxIdelTime not emitted verbatim: %q", got)
	}
	if strings.Contains(got, "MaxIdleTime") {
		t.Fatalf("spelling was corrected, firmware expects MaxIdelTime: %q", got)
	}
}

func TestParseAddrHelpers(t *testing.T) {
	if got := parseAddr("10.115.89.118"); got != netip.MustParseAddr("10.115.89.118") {
		t.Fatalf("parseAddr = %v", got)
	}
	if got := parseAddr(""); got.IsValid() {
		t.Fatalf("empty address should be invalid, got %v", got)
	}
	if got := addrString(netip.Addr{}); got != "" {
		t.Fatalf("addrString of zero addr = %q", got)
	}
	if got := netmaskAddr(netip.Addr{}); got != netip.MustParseAddr("255.255.255.0") {
		t.Fatalf("netmaskAddr fallback = %v", got)
	}
}

func TestRootElement(t *testing.T) {
	if got := rootElement(Fixture("error_125002.xml")); got != "error" {
		t.Fatalf("root of error fixture = %q", got)
	}
	if got := rootElement(Fixture("device_information.xml")); got != "response" {
		t.Fatalf("root of response fixture = %q", got)
	}
}
