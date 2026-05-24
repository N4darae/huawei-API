//go:build !linux

package enroll

import "github.com/n4darae/huawei-API/src/internal/domain"

func ioctlReset(path string) error {
	return domain.UnsupportedOn("USBDEVFS_RESET")
}
