//go:build unix

package proxysup

import (
	"os"
	"syscall"
)

func fileGID(info os.FileInfo) (int, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Gid), true
}

func processGroups() ([]int, error) {
	gids, err := os.Getgroups()
	if err != nil {
		return nil, err
	}
	return append(gids, os.Getegid(), os.Getgid()), nil
}
