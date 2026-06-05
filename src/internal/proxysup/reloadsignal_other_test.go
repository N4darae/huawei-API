//go:build !unix

package proxysup

import (
	"os/exec"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

func sendReloadSignal(*exec.Cmd) error {
	return domain.UnsupportedOn("SIGUSR1 reload signal")
}
