//go:build !linux

package enroll

import "github.com/n4darae/huawei-API/src/internal/domain"

func CountConntrack() (int, error) {
	return 0, domain.UnsupportedOn("NETLINK_NETFILTER conntrack dump")
}
