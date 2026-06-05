//go:build !unix

package proxysup

import "os"

func fileGID(os.FileInfo) (int, bool) {
	return 0, false
}

func processGroups() ([]int, error) {
	return nil, nil
}
