package fw

import (
	"encoding/binary"
	"net/netip"
	"testing"
)

func TestNfMsgTypeEncodesTheSubsystem(t *testing.T) {
	if got := nfMsgType(nfnlSubsysCtnetlink, ipctnlMsgCtGet); got != 0x0101 {
		t.Fatalf("ct get message type %#x", got)
	}
	if got := nfMsgType(nfnlSubsysCtnetlink, ipctnlMsgCtDelete); got != 0x0102 {
		t.Fatalf("ct delete message type %#x", got)
	}
}

func TestEncodeNfGenMsg(t *testing.T) {
	b := encodeNfGenMsg(2)
	if len(b) != sizeofNfGenMsg {
		t.Fatalf("nfgenmsg is %d bytes, want %d", len(b), sizeofNfGenMsg)
	}
	if b[0] != 2 || b[1] != 0 {
		t.Fatalf("family/version %d/%d", b[0], b[1])
	}
	if binary.BigEndian.Uint16(b[2:4]) != 0 {
		t.Fatal("res_id must be zero")
	}
}

func TestParseNfAttrsStripsTheNestedFlag(t *testing.T) {
	inner := encodeNfAttr(ctaIPV4Src, []byte{192, 168, 101, 100})
	ipAttr := encodeNfAttr(ctaTupleIP|nlaFNested, inner)
	orig := encodeNfAttr(ctaTupleOrig|nlaFNested, ipAttr)

	attrs := parseNfAttrs(orig)
	if len(attrs) != 1 {
		t.Fatalf("want one attribute, got %d", len(attrs))
	}
	if attrs[0].Type != ctaTupleOrig {
		t.Fatalf("the nested flag must be stripped from the type, got %#x", attrs[0].Type)
	}
	if len(attrs[0].Raw) != len(orig) {
		t.Fatal("the raw bytes must be preserved so a delete can echo them back verbatim")
	}
}

func TestTupleSourceFindsTheOriginalSourceAddress(t *testing.T) {
	inner := encodeNfAttr(ctaIPV4Src, []byte{192, 168, 101, 100})
	inner = append(inner, encodeNfAttr(2, []byte{8, 8, 8, 8})...)
	ipAttr := encodeNfAttr(ctaTupleIP|nlaFNested, inner)

	got, ok := tupleSource(ipAttr)
	if !ok {
		t.Fatal("source address not found")
	}
	if got != netip.MustParseAddr("192.168.101.100") {
		t.Fatalf("source %v", got)
	}
}

func TestTupleSourceHandlesIPv6(t *testing.T) {
	addr := netip.MustParseAddr("2001:db8::1").As16()
	inner := encodeNfAttr(ctaIPV6Src, addr[:])
	ipAttr := encodeNfAttr(ctaTupleIP|nlaFNested, inner)
	got, ok := tupleSource(ipAttr)
	if !ok || got != netip.MustParseAddr("2001:db8::1") {
		t.Fatalf("ipv6 source %v ok=%v", got, ok)
	}
}

func TestTupleSourceRejectsAnEmptyTuple(t *testing.T) {
	if _, ok := tupleSource(nil); ok {
		t.Fatal("an empty tuple has no source")
	}
	if _, ok := tupleSource(encodeNfAttr(99, []byte{1, 2, 3, 4})); ok {
		t.Fatal("a tuple without an ip attribute has no source")
	}
}

func TestFindNfAttr(t *testing.T) {
	b := encodeNfAttr(ctaID, []byte{0, 0, 0, 7})
	attrs := parseNfAttrs(b)
	if _, ok := findNfAttr(attrs, ctaID); !ok {
		t.Fatal("ctaID not found")
	}
	if _, ok := findNfAttr(attrs, ctaTupleOrig); ok {
		t.Fatal("an absent attribute must not be reported as found")
	}
}
