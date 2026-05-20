package fw

import (
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"runtime"
	"testing"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

const afUnix = 1

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
	req := encodeDiagReq(afInet, protoTCP, 0x1234, id)
	if len(req) != sizeofDiagReq {
		t.Fatalf("request is %d bytes, want %d", len(req), sizeofDiagReq)
	}
	if req[0] != afInet || req[1] != protoTCP {
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
	msg[0] = afInet
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
	msg[0] = afUnix
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

func TestKillSocketsRefusesOffLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("linux opens a real netlink socket, covered by sockdestroy_linux_test.go")
	}
	n := NewNft(Options{Exec: newFakeNft().exec})
	_, err := n.KillSockets(context.Background(), netip.MustParseAddr("192.0.2.222"))
	if !errors.Is(err, domain.ErrUnsupportedPlatform) {
		t.Fatalf("a fence that cannot kill sockets must say so, got %v", err)
	}
}
