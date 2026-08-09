//go:build !linux

package proxysup

import "syscall"

func netnsSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}

func scratchSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}

func killProcessGroup(int) {}
