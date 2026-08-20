//go:build !linux

package fw

import (
	"context"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

type netlinkConn struct{}

func dialNetlink(int) (*netlinkConn, error) {
	return nil, domain.UnsupportedOn("netlink sockets")
}

func (c *netlinkConn) Close() error { return nil }

func (c *netlinkConn) dump(context.Context, uint16, []byte) ([][]byte, error) {
	return nil, domain.UnsupportedOn("netlink sockets")
}

func (c *netlinkConn) request(context.Context, uint16, []byte) error {
	return domain.UnsupportedOn("netlink sockets")
}
