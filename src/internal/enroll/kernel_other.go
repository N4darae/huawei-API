//go:build !linux

package enroll

import "github.com/n4darae/huawei-API/src/internal/domain"

func KernelRelease() (string, error) {
	return "", domain.UnsupportedOn("kernel release probe")
}
