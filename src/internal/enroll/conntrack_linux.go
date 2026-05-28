//go:build linux

package enroll

import (
	"encoding/binary"
	"fmt"
	"syscall"
)

const (
	netlinkNetfilter    = 12
	nfnlSubsysCtnetlink = 1
	ipctnlMsgCtGet      = 1
	nfnetlinkV0         = 0

	ctAfInet = 2

	ctNlmsgError = 0x2
	ctNlmsgDone  = 0x3

	ctNlmFRequest = 0x001
	ctNlmFDump    = 0x300

	ctSizeofNlMsgHdr = 16
)

func CountConntrack() (int, error) {
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, netlinkNetfilter)
	if err != nil {
		return 0, fmt.Errorf("socket(NETLINK_NETFILTER): %w", err)
	}
	defer syscall.Close(fd)
	if err := syscall.Bind(fd, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}); err != nil {
		return 0, fmt.Errorf("bind(NETLINK_NETFILTER): %w", err)
	}
	tv := syscall.Timeval{Sec: 3}
	if err := syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv); err != nil {
		return 0, err
	}

	body := []byte{ctAfInet, nfnetlinkV0, 0, 0}
	msg := make([]byte, ctSizeofNlMsgHdr+len(body))
	binary.NativeEndian.PutUint32(msg[0:4], uint32(len(msg)))
	binary.NativeEndian.PutUint16(msg[4:6], nfnlSubsysCtnetlink<<8|ipctnlMsgCtGet)
	binary.NativeEndian.PutUint16(msg[6:8], ctNlmFRequest|ctNlmFDump)
	binary.NativeEndian.PutUint32(msg[8:12], 1)
	copy(msg[ctSizeofNlMsgHdr:], body)

	if err := syscall.Sendto(fd, msg, 0, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}); err != nil {
		return 0, fmt.Errorf("sendto(CTNETLINK dump): %w", err)
	}

	buf := make([]byte, 1<<17)
	count := 0
	for {
		n, _, err := syscall.Recvfrom(fd, buf, 0)
		if err != nil {
			return count, fmt.Errorf("recvfrom(CTNETLINK dump): %w", err)
		}
		msgs, err := syscall.ParseNetlinkMessage(buf[:n])
		if err != nil {
			return count, err
		}
		for _, m := range msgs {
			switch m.Header.Type {
			case ctNlmsgDone:
				return count, nil
			case ctNlmsgError:
				if e := netlinkErrno(m.Data); e != 0 {
					return count, fmt.Errorf("CTNETLINK dump: %w", e)
				}
				return count, nil
			default:
				count++
			}
		}
	}
}

func netlinkErrno(data []byte) syscall.Errno {
	if len(data) < 4 {
		return syscall.EINVAL
	}
	code := int32(binary.NativeEndian.Uint32(data[0:4]))
	if code == 0 {
		return 0
	}
	return syscall.Errno(-code)
}
