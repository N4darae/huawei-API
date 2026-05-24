package secrets

import (
	"fmt"
	"io/fs"
)

func checkKEKPermissions(info fs.FileInfo, path string) error {
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%w: %s is mode %04o", ErrKEKPermissions, path, info.Mode().Perm())
	}
	return nil
}
