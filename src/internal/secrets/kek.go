package secrets

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const KEKFileMode fs.FileMode = 0o600

var ErrKEKPermissions = errors.New("secrets: kek file must not be readable by group or other; chmod 0600 it")

func GenerateKEK() ([]byte, error) {
	kek := make([]byte, KEKSize)
	if _, err := rand.Read(kek); err != nil {
		return nil, err
	}
	return kek, nil
}

func WriteKEK(path string, kek []byte) error {
	if len(kek) != KEKSize {
		return fmt.Errorf("%w: got %d bytes", ErrKEKSize, len(kek))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("secrets: cannot create %s: %w", filepath.Dir(path), err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(hex.EncodeToString(kek)+"\n"), KEKFileMode); err != nil {
		return fmt.Errorf("secrets: cannot write %s: %w", tmp, err)
	}
	if err := os.Chmod(tmp, KEKFileMode); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("secrets: cannot chmod %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("secrets: cannot install %s: %w", path, err)
	}
	return nil
}

func ReadKEK(path string) ([]byte, error) {
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("%w: %s does not exist", ErrKEKMissing, path)
	case errors.Is(err, fs.ErrPermission):
		return nil, fmt.Errorf("%w: %s is not readable by this user", ErrKEKMissing, path)
	case err != nil:
		return nil, fmt.Errorf("%w: %s: %v", ErrKEKMissing, path, err)
	case info.IsDir():
		return nil, fmt.Errorf("%w: %s is a directory", ErrKEKMissing, path)
	}
	if err := checkKEKPermissions(info, path); err != nil {
		return nil, err
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrKEKMissing, path, err)
	}
	kek, err := decodeKEK(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return kek, nil
}

func LoadKEK(path string) (Sealer, error) {
	kek, err := ReadKEK(path)
	if err != nil {
		return nil, err
	}
	return NewSealer(kek)
}

func decodeKEK(raw []byte) ([]byte, error) {
	if len(raw) == KEKSize {
		return append([]byte(nil), raw...), nil
	}
	text := string(bytes.TrimSpace(raw))
	if len(text) == 0 {
		return nil, fmt.Errorf("%w: file is empty", ErrKEKSize)
	}
	if b, err := hex.DecodeString(text); err == nil && len(b) == KEKSize {
		return b, nil
	}
	if b, err := base64.StdEncoding.DecodeString(text); err == nil && len(b) == KEKSize {
		return b, nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(text); err == nil && len(b) == KEKSize {
		return b, nil
	}
	return nil, fmt.Errorf("%w: file holds %d bytes and is neither raw, hex nor base64 encoded 32 byte key material", ErrKEKSize, len(raw))
}
