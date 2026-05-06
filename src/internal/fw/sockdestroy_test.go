package fw

import (
	"context"
	"encoding/binary"
	"net"
	"net/netip"
	"syscall"
	"testing"
)

func TestKillableStatesExcludesListen(t *testing.T) {
	mask := killableStates()
	if mask&(1<<tcpListen) != 0 {
		t.Fatal("killing a listening socket would take the proxy down instead of a customer connection")
	}
	if mask&(1<<tcpEstablished) == 0 {
		t.Fatal("established sockets are the whole point of the fence")
	}
}

func TestEncodeDiagReqLayout(t *testing.T) {
	id := make([]byte, sizeofSockID)
	for i := range id {
		id[i] = byte(i)
	}
	req := encodeDiagReq(syscall.AF_INET, syscall.IPPROTO_TCP, 0x1234, id)
	if len(req) != sizeofDiagReq {
		t.Fatalf("request is %d bytes, want %d", len(req), sizeofDiagReq)
	}
	if req[0] != syscall.AF_INET || req[1] != syscall.IPPROTO_TCP {
		t.Fatalf("family/protocol %d/%d", req[0], req[1])
	}
	if got := binary.NativeEndian.Uint32(req[4:8]); got != 0x1234 {
		t.Fatalf("states %x", got)
	}
	for i := range id {
		if req[8+i] != id[i] {
			t.Fatalf("sockid byte %d not copied verbatim", i)
		}
	}
}

func TestParseDiagMsgRoundTrip(t *testing.T) {
	msg := make([]byte, sizeofDiagMsg)
	msg[0] = syscall.AF_INET
	msg[1] = tcpEstablished
	id := msg[4 : 4+sizeofSockID]
	binary.BigEndian.PutUint16(id[0:2], 34567)
	binary.BigEndian.PutUint16(id[2:4], 80)
	copy(id[4:8], []byte{192, 168, 101, 100})
	copy(id[20:24], []byte{8, 8, 8, 8})
	binary.NativeEndian.PutUint32(msg[4+sizeofSockID+12:4+sizeofSockID+16], 6101)
	e, ok := parseDiagMsg(msg)
	if !ok {
		t.Fatal("parse failed")
	}
	if e.Src != netip.MustParseAddr("192.168.101.100") || e.Dst != netip.MustParseAddr("8.8.8.8") {
		t.Fatalf("addresses %v -> %v", e.Src, e.Dst)
	}
	if e.SPort != 34567 || e.DPort != 80 {
		t.Fatalf("ports %d -> %d", e.SPort, e.DPort)
	}
	if e.UID != 6101 {
		t.Fatalf("uid %d", e.UID)
	}
	if len(e.ID) != sizeofSockID {
		t.Fatalf("sockid length %d", len(e.ID))
	}
}

func TestParseDiagMsgRejectsShortInput(t *testing.T) {
	if _, ok := parseDiagMsg(make([]byte, 8)); ok {
		t.Fatal("a truncated message must be rejected")
	}
	msg := make([]byte, sizeofDiagMsg)
	msg[0] = syscall.AF_UNIX
	if _, ok := parseDiagMsg(msg); ok {
		t.Fatal("a non-inet family must be rejected")
	}
}

func TestKillSocketsRejectsTheZeroAddress(t *testing.T) {
	n := NewNft(Options{Exec: newFakeNft().exec})
	if _, err := n.KillSockets(context.Background(), netip.Addr{}); err != ErrBadAddr {
		t.Fatalf("want ErrBadAddr, got %v", err)
	}
	if _, err := n.FlushConntrack(context.Background(), netip.Addr{}); err != ErrBadAddr {
		t.Fatalf("want ErrBadAddr, got %v", err)
	}
}

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
