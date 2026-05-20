package fw

import (
	"context"
	"net"
	"net/netip"
	"testing"
)

func TestKillSocketsReturnsZeroForAnAddressWithNoSockets(t *testing.T) {
	n := NewNft(Options{Exec: newFakeNft().exec})
	killed, err := n.KillSockets(context.Background(), netip.MustParseAddr("192.0.2.222"))
	if err != nil {
		t.Fatalf("KillSockets: %v", err)
	}
	if killed != 0 {
		t.Fatalf("want a real zero, got %d", killed)
	}
}

func TestSocketDiagSeesALoopbackConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen: %v", err)
	}
	defer ln.Close()
	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()
	server, err := ln.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	defer server.Close()

	got, err := CountEstablishedFrom(netip.MustParseAddr("127.0.0.1"))
	if err != nil {
		t.Fatalf("CountEstablishedFrom: %v", err)
	}
	if got < 2 {
		t.Fatalf("the diag dump must see both ends of a loopback connection, got %d", got)
	}
}
