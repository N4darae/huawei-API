//go:build !linux

package secrets

import "io/fs"

func checkKEKPermissions(fs.FileInfo, string) error {
	return nil
}
