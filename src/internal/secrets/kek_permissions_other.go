//go:build !unix

package secrets

import "io/fs"

func checkKEKPermissions(fs.FileInfo, string) error {
	return nil
}
