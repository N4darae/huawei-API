//go:build unix

package secrets

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteKEKSetsRestrictiveMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "kek.cred")
	kek := testKEK(t)
	if err := WriteKEK(path, kek); err != nil {
		t.Fatalf("WriteKEK: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != KEKFileMode {
		t.Fatalf("kek file is mode %04o, want %04o", info.Mode().Perm(), KEKFileMode)
	}
}

func TestLoadKEKRejectsWorldReadableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kek.cred")
	kek := testKEK(t)
	if err := os.WriteFile(path, []byte(hex.EncodeToString(kek)), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadKEK(path)
	if !errors.Is(err, ErrKEKPermissions) {
		t.Fatalf("LoadKEK on a 0644 file returned %v, want ErrKEKPermissions", err)
	}
}
