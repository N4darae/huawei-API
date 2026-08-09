package proxysup

import "syscall"

func netnsSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWNET,
		Setpgid:    true,
		Pdeathsig:  syscall.SIGKILL,
	}
}

func scratchSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}

func killProcessGroup(pid int) {
	syscall.Kill(-pid, syscall.SIGKILL)
}
