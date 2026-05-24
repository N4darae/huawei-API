//go:build linux

package enroll

import (
	"fmt"
	"os"
	"syscall"
)

const usbdevfsReset = 0x5514

func ioctlReset(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("enroll: open %s: %w", path, err)
	}
	defer f.Close()
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), usbdevfsReset, 0); errno != 0 {
		return fmt.Errorf("enroll: USBDEVFS_RESET on %s: %w", path, errno)
	}
	return nil
}
