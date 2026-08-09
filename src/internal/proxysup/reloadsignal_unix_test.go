//go:build unix

package proxysup

import (
	"os/exec"
	"syscall"
)

func sendReloadSignal(cmd *exec.Cmd) error {
	return cmd.Process.Signal(syscall.SIGUSR1)
}
