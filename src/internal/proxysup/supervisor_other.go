//go:build !linux

package proxysup

import "os"

func fileGID(os.FileInfo) (int, bool) {
	return 0, false
}
