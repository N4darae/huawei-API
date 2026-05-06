package fw

import (
	"context"
	"encoding/binary"
	"net/netip"
	"syscall"
)

const (
	nfnlSubsysCtnetlink = 1

	ipctnlMsgCtGet    = 1
	ipctnlMsgCtDelete = 2

	ctaTupleOrig = 1
	ctaID        = 12

	ctaTupleIP = 1

	ctaIPV4Src = 1
	ctaIPV6Src = 3

	nlaFNested = 0x8000

	sizeofNfGenMsg = 4
	sizeofNlAttr   = 4
)

func nfMsgType(subsys, msg uint16) uint16 { return subsys<<8 | msg }

func encodeNfGenMsg(family uint8) []byte {
	b := make([]byte, sizeofNfGenMsg)
	b[0] = family
	b[1] = 0
	binary.BigEndian.PutUint16(b[2:4], 0)
	return b
}

type nfAttr struct {
	Type uint16
	Data []byte
	Raw  []byte
}

func parseNfAttrs(b []byte) []nfAttr {
	var out []nfAttr
	for len(b) >= sizeofNlAttr {
		length := int(binary.NativeEndian.Uint16(b[0:2]))
		kind := binary.NativeEndian.Uint16(b[2:4])
		if length < sizeofNlAttr || length > len(b) {
			return out
		}
		out = append(out, nfAttr{
			Type: kind &^ nlaFNested,
			Data: b[sizeofNlAttr:length],
			Raw:  b[:length],
		})
		aligned := (length + 3) &^ 3
		if aligned >= len(b) {
			return out
		}
		b = b[aligned:]
	}
	return out
}

func findNfAttr(attrs []nfAttr, kind uint16) (nfAttr, bool) {
	for _, a := range attrs {
		if a.Type == kind {
			return a, true
		}
	}
	return nfAttr{}, false
}

func encodeNfAttr(kind uint16, data []byte) []byte {
	length := sizeofNlAttr + len(data)
	buf := make([]byte, (length+3)&^3)
	binary.NativeEndian.PutUint16(buf[0:2], uint16(length))
	binary.NativeEndian.PutUint16(buf[2:4], kind)
	copy(buf[sizeofNlAttr:], data)
	return buf
}

type conntrackEntry struct {
	Src   netip.Addr
	Tuple []byte
	ID    []byte
}

func tupleSource(tuple []byte) (netip.Addr, bool) {
	ipAttr, ok := findNfAttr(parseNfAttrs(tuple), ctaTupleIP)
	if !ok {
		return netip.Addr{}, false
	}
	for _, a := range parseNfAttrs(ipAttr.Data) {
		switch a.Type {
		case ctaIPV4Src:
			if len(a.Data) == 4 {
				return netip.AddrFrom4([4]byte(a.Data[0:4])), true
			}
		case ctaIPV6Src:
			if len(a.Data) == 16 {
				return netip.AddrFrom16([16]byte(a.Data[0:16])).Unmap(), true
			}
		}
	}
	return netip.Addr{}, false
}

func listConntrack(family uint8) ([]conntrackEntry, error) {
	c, err := dialNetlink(netlinkNetfil)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	payloads, err := c.dump(nfMsgType(nfnlSubsysCtnetlink, ipctnlMsgCtGet), encodeNfGenMsg(family))
	if err != nil {
		return nil, err
	}
	var out []conntrackEntry
	for _, p := range payloads {
		if len(p) < sizeofNfGenMsg {
			continue
		}
		attrs := parseNfAttrs(p[sizeofNfGenMsg:])
		orig, ok := findNfAttr(attrs, ctaTupleOrig)
		if !ok {
			continue
		}
		src, ok := tupleSource(orig.Data)
		if !ok {
			continue
		}
		e := conntrackEntry{Src: src, Tuple: append([]byte(nil), orig.Raw...)}
		if id, ok := findNfAttr(attrs, ctaID); ok && len(id.Data) == 4 {
			e.ID = append([]byte(nil), id.Raw...)
		}
		out = append(out, e)
	}
	return out, nil
}

func (n *Nft) FlushConntrack(ctx context.Context, src netip.Addr) (int, error) {
	if !src.IsValid() {
		return 0, ErrBadAddr
	}
	family := familyOf(src)
	entries, err := listConntrack(family)
	if err != nil {
		if IsAbsent(err) {
			return 0, nil
		}
		return 0, err
	}
	var targets []conntrackEntry
	for _, e := range entries {
		if e.Src == src {
			targets = append(targets, e)
		}
	}
	if len(targets) == 0 {
		return 0, nil
	}
	c, err := dialNetlink(netlinkNetfil)
	if err != nil {
		return 0, err
	}
	defer c.Close()
	deleted := 0
	for _, t := range targets {
		if ctx.Err() != nil {
			return deleted, ctx.Err()
		}
		payload := encodeNfGenMsg(family)
		payload = append(payload, t.Tuple...)
		payload = append(payload, t.ID...)
		if err := c.request(nfMsgType(nfnlSubsysCtnetlink, ipctnlMsgCtDelete), payload); err != nil {
			if err == syscall.ENOENT {
				continue
			}
			continue
		}
		deleted++
	}
	return deleted, nil
}
