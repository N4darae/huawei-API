package fw

import (
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"syscall"
)

const (
	netlinkInetDiag = 4
	netlinkNetfil   = 12

	sockDiagByFamily = 20
	sockDestroy      = 21

	sizeofSockID   = 48
	sizeofDiagReq  = 8 + sizeofSockID
	sizeofDiagMsg  = 4 + sizeofSockID + 20
	sizeofNlMsgHdr = syscall.NLMSG_HDRLEN

	tcpEstablished = 1
	tcpListen      = 10
	tcpMaxStates   = 13
)

var ErrMalformedNetlink = errors.New("fw: malformed netlink message")

func killableStates() uint32 {
	var mask uint32
	for s := 1; s < tcpMaxStates; s++ {
		if s == tcpListen {
			continue
		}
		mask |= 1 << uint(s)
	}
	return mask
}

type netlinkConn struct {
	fd  int
	seq uint32
}

func dialNetlink(proto int) (*netlinkConn, error) {
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, proto)
	if err != nil {
		return nil, err
	}
	if err := syscall.Bind(fd, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}); err != nil {
		syscall.Close(fd)
		return nil, err
	}
	tv := syscall.Timeval{Sec: 5}
	if err := syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv); err != nil {
		syscall.Close(fd)
		return nil, err
	}
	return &netlinkConn{fd: fd}, nil
}

func (c *netlinkConn) Close() error { return syscall.Close(c.fd) }

func (c *netlinkConn) send(kind uint16, flags uint16, payload []byte) error {
	c.seq++
	msg := make([]byte, sizeofNlMsgHdr+len(payload))
	binary.NativeEndian.PutUint32(msg[0:4], uint32(len(msg)))
	binary.NativeEndian.PutUint16(msg[4:6], kind)
	binary.NativeEndian.PutUint16(msg[6:8], flags)
	binary.NativeEndian.PutUint32(msg[8:12], c.seq)
	copy(msg[sizeofNlMsgHdr:], payload)
	return syscall.Sendto(c.fd, msg, 0, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK})
}

func (c *netlinkConn) dump(kind uint16, payload []byte) ([][]byte, error) {
	if err := c.send(kind, syscall.NLM_F_REQUEST|syscall.NLM_F_DUMP, payload); err != nil {
		return nil, err
	}
	var out [][]byte
	for {
		buf := make([]byte, 1<<17)
		n, _, err := syscall.Recvfrom(c.fd, buf, 0)
		if err != nil {
			return nil, err
		}
		if n < sizeofNlMsgHdr {
			return nil, ErrMalformedNetlink
		}
		msgs, err := syscall.ParseNetlinkMessage(buf[:n])
		if err != nil {
			return nil, err
		}
		done := false
		for _, m := range msgs {
			switch m.Header.Type {
			case syscall.NLMSG_DONE:
				done = true
			case syscall.NLMSG_ERROR:
				if err := decodeNetlinkError(m.Data); err != nil {
					return nil, err
				}
				done = true
			default:
				out = append(out, m.Data)
			}
		}
		if done {
			break
		}
	}
	return out, nil
}

func (c *netlinkConn) request(kind uint16, payload []byte) error {
	if err := c.send(kind, syscall.NLM_F_REQUEST|syscall.NLM_F_ACK, payload); err != nil {
		return err
	}
	for {
		buf := make([]byte, 1<<16)
		n, _, err := syscall.Recvfrom(c.fd, buf, 0)
		if err != nil {
			return err
		}
		if n < sizeofNlMsgHdr {
			return ErrMalformedNetlink
		}
		msgs, err := syscall.ParseNetlinkMessage(buf[:n])
		if err != nil {
			return err
		}
		for _, m := range msgs {
			switch m.Header.Type {
			case syscall.NLMSG_ERROR:
				return decodeNetlinkError(m.Data)
			case syscall.NLMSG_DONE:
				return nil
			}
		}
	}
}

func decodeNetlinkError(data []byte) error {
	if len(data) < 4 {
		return ErrMalformedNetlink
	}
	code := int32(binary.NativeEndian.Uint32(data[0:4]))
	if code == 0 {
		return nil
	}
	return syscall.Errno(-code)
}

type inetDiagEntry struct {
	Family uint8
	State  uint8
	Src    netip.Addr
	Dst    netip.Addr
	SPort  uint16
	DPort  uint16
	UID    uint32
	Inode  uint32
	ID     []byte
}

func encodeDiagReq(family, protocol uint8, states uint32, id []byte) []byte {
	req := make([]byte, sizeofDiagReq)
	req[0] = family
	req[1] = protocol
	binary.NativeEndian.PutUint32(req[4:8], states)
	if len(id) == sizeofSockID {
		copy(req[8:], id)
	}
	return req
}

func parseDiagMsg(data []byte) (inetDiagEntry, bool) {
	if len(data) < sizeofDiagMsg {
		return inetDiagEntry{}, false
	}
	e := inetDiagEntry{Family: data[0], State: data[1]}
	id := data[4 : 4+sizeofSockID]
	e.ID = append([]byte(nil), id...)
	e.SPort = binary.BigEndian.Uint16(id[0:2])
	e.DPort = binary.BigEndian.Uint16(id[2:4])
	switch e.Family {
	case syscall.AF_INET:
		e.Src = netip.AddrFrom4([4]byte(id[4:8]))
		e.Dst = netip.AddrFrom4([4]byte(id[20:24]))
	case syscall.AF_INET6:
		e.Src = netip.AddrFrom16([16]byte(id[4:20])).Unmap()
		e.Dst = netip.AddrFrom16([16]byte(id[20:36])).Unmap()
	default:
		return inetDiagEntry{}, false
	}
	e.UID = binary.NativeEndian.Uint32(data[4+sizeofSockID+12 : 4+sizeofSockID+16])
	e.Inode = binary.NativeEndian.Uint32(data[4+sizeofSockID+16 : 4+sizeofSockID+20])
	return e, true
}

func familyOf(a netip.Addr) uint8 {
	if a.Is4() {
		return syscall.AF_INET
	}
	return syscall.AF_INET6
}

func listSockets(family uint8, states uint32) ([]inetDiagEntry, error) {
	c, err := dialNetlink(netlinkInetDiag)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	payloads, err := c.dump(sockDiagByFamily, encodeDiagReq(family, syscall.IPPROTO_TCP, states, nil))
	if err != nil {
		return nil, err
	}
	out := make([]inetDiagEntry, 0, len(payloads))
	for _, p := range payloads {
		if e, ok := parseDiagMsg(p); ok {
			out = append(out, e)
		}
	}
	return out, nil
}

func SocketsFrom(src netip.Addr) ([]inetDiagEntry, error) {
	if !src.IsValid() {
		return nil, ErrBadAddr
	}
	all, err := listSockets(familyOf(src), killableStates())
	if err != nil {
		return nil, err
	}
	var out []inetDiagEntry
	for _, e := range all {
		if e.Src == src {
			out = append(out, e)
		}
	}
	return out, nil
}

func HasListener(addr netip.Addr, port int) (bool, error) {
	if !addr.IsValid() {
		return false, ErrBadAddr
	}
	entries, err := listSockets(familyOf(addr), 1<<tcpListen)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if int(e.SPort) != port {
			continue
		}
		if e.Src == addr || e.Src.IsUnspecified() {
			return true, nil
		}
	}
	return false, nil
}

func CountEstablishedFrom(src netip.Addr) (int, error) {
	socks, err := SocketsFrom(src)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, s := range socks {
		if s.State == tcpEstablished {
			n++
		}
	}
	return n, nil
}

func (n *Nft) KillSockets(ctx context.Context, src netip.Addr) (int, error) {
	if !src.IsValid() {
		return 0, ErrBadAddr
	}
	targets, err := SocketsFrom(src)
	if err != nil {
		return 0, err
	}
	if len(targets) == 0 {
		return 0, nil
	}
	c, err := dialNetlink(netlinkInetDiag)
	if err != nil {
		return 0, err
	}
	defer c.Close()
	killed := 0
	for _, t := range targets {
		if ctx.Err() != nil {
			return killed, ctx.Err()
		}
		req := encodeDiagReq(t.Family, syscall.IPPROTO_TCP, 0, t.ID)
		if err := c.request(sockDestroy, req); err != nil {
			continue
		}
		killed++
	}
	return killed, nil
}
