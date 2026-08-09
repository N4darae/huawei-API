package secrets

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testKEK(t *testing.T) []byte {
	t.Helper()
	kek, err := GenerateKEK()
	if err != nil {
		t.Fatalf("GenerateKEK: %v", err)
	}
	if len(kek) != KEKSize {
		t.Fatalf("GenerateKEK returned %d bytes, want %d", len(kek), KEKSize)
	}
	return kek
}

const leakProbeMinLen = 8

func TestSealOpenRoundTrip(t *testing.T) {
	s, err := NewSealer(testKEK(t))
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}

	for _, plaintext := range []string{"", "a", "Kq7mZr2xTn9wLb4V", strings.Repeat("x", 4096)} {
		ct, err := s.Seal([]byte(plaintext))
		if err != nil {
			t.Fatalf("Seal(%d bytes): %v", len(plaintext), err)
		}
		if len(ct) <= NonceSize {
			t.Fatalf("ciphertext %d bytes must exceed the %d byte nonce", len(ct), NonceSize)
		}
		if len(plaintext) >= leakProbeMinLen && bytes.Contains(ct, []byte(plaintext)) {
			t.Fatal("ciphertext leaks the plaintext")
		}
		got, err := s.Open(ct)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if string(got) != plaintext {
			t.Fatalf("round trip returned %q, want %q", got, plaintext)
		}
	}
}

func TestSealIsRandomised(t *testing.T) {
	s, err := NewSealer(testKEK(t))
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	a, err := s.Seal([]byte("same"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	b, err := s.Seal([]byte("same"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("two seals of the same plaintext produced an identical ciphertext; the nonce is not random")
	}
}

func TestOpenRejectsTamperedCiphertext(t *testing.T) {
	s, err := NewSealer(testKEK(t))
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	ct, err := s.Seal([]byte("Kq7mZr2xTn9wLb4V"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	cases := map[string][]byte{
		"flipped nonce byte":       flip(ct, 0),
		"flipped body byte":        flip(ct, NonceSize+1),
		"flipped tag byte":         flip(ct, len(ct)-1),
		"truncated":                ct[:len(ct)-1],
		"shorter than the nonce":   ct[:NonceSize-1],
		"empty":                    {},
		"nonce only, no tag":       ct[:NonceSize],
		"sealed under a wrong key": sealWithOtherKey(t, "Kq7mZr2xTn9wLb4V"),
	}
	for name, bad := range cases {
		if _, err := s.Open(bad); !errors.Is(err, ErrOpenFailed) {
			t.Errorf("Open(%s) returned %v, want ErrOpenFailed", name, err)
		}
	}
}

func flip(b []byte, i int) []byte {
	out := append([]byte(nil), b...)
	out[i] ^= 0x40
	return out
}

func sealWithOtherKey(t *testing.T, plaintext string) []byte {
	t.Helper()
	s, err := NewSealer(testKEK(t))
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	ct, err := s.Seal([]byte(plaintext))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	return ct
}

func TestNewSealerRejectsWrongKeySize(t *testing.T) {
	for _, n := range []int{0, 1, 16, 31, 33, 64} {
		if _, err := NewSealer(make([]byte, n)); !errors.Is(err, ErrKEKSize) {
			t.Errorf("NewSealer(%d bytes) returned %v, want ErrKEKSize", n, err)
		}
	}
}

func TestUninitialisedSealerDoesNotPanic(t *testing.T) {
	var b *box
	if _, err := b.Seal([]byte("x")); !errors.Is(err, ErrNotUnsealed) {
		t.Errorf("Seal on a nil sealer returned %v, want ErrNotUnsealed", err)
	}
	if _, err := b.Open(make([]byte, 64)); !errors.Is(err, ErrNotUnsealed) {
		t.Errorf("Open on a nil sealer returned %v, want ErrNotUnsealed", err)
	}
}

func TestLoadKEKRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "kek.cred")
	kek := testKEK(t)
	if err := WriteKEK(path, kek); err != nil {
		t.Fatalf("WriteKEK: %v", err)
	}

	got, err := ReadKEK(path)
	if err != nil {
		t.Fatalf("ReadKEK: %v", err)
	}
	if !bytes.Equal(got, kek) {
		t.Fatal("ReadKEK returned different key material")
	}

	sealer, err := LoadKEK(path)
	if err != nil {
		t.Fatalf("LoadKEK: %v", err)
	}
	ct, err := sealer.Seal([]byte("secret"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	reopened, err := LoadKEK(path)
	if err != nil {
		t.Fatalf("LoadKEK again: %v", err)
	}
	pt, err := reopened.Open(ct)
	if err != nil {
		t.Fatalf("Open with a reloaded sealer: %v", err)
	}
	if string(pt) != "secret" {
		t.Fatalf("reloaded sealer returned %q", pt)
	}
}

func TestLoadKEKAcceptsRawHexAndBase64(t *testing.T) {
	kek := testKEK(t)
	cases := map[string][]byte{
		"raw":        kek,
		"hex":        []byte(hex.EncodeToString(kek)),
		"hex nl":     []byte(hex.EncodeToString(kek) + "\n"),
		"base64":     []byte(base64.StdEncoding.EncodeToString(kek)),
		"base64 nl":  []byte(base64.StdEncoding.EncodeToString(kek) + "\n"),
		"base64 raw": []byte(base64.RawStdEncoding.EncodeToString(kek)),
	}
	for name, body := range cases {
		path := filepath.Join(t.TempDir(), "kek.cred")
		if err := os.WriteFile(path, body, KEKFileMode); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		got, err := ReadKEK(path)
		if err != nil {
			t.Errorf("ReadKEK(%s): %v", name, err)
			continue
		}
		if !bytes.Equal(got, kek) {
			t.Errorf("ReadKEK(%s) returned different key material", name)
		}
	}
}

func TestLoadKEKMissingIsActionable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kek.cred")
	_, err := LoadKEK(path)
	if !errors.Is(err, ErrKEKMissing) {
		t.Fatalf("LoadKEK on a missing file returned %v, want ErrKEKMissing", err)
	}
	if !strings.Contains(err.Error(), "restore-kek") {
		t.Errorf("error %q does not tell the operator what to run", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name the path it looked at", err)
	}
}

func TestLoadKEKRejectsDirectory(t *testing.T) {
	if _, err := LoadKEK(t.TempDir()); !errors.Is(err, ErrKEKMissing) {
		t.Fatalf("LoadKEK on a directory returned %v, want ErrKEKMissing", err)
	}
}

func TestLoadKEKRejectsGarbage(t *testing.T) {
	for name, body := range map[string]string{
		"empty":       "",
		"whitespace":  "   \n",
		"short hex":   "deadbeef",
		"not encoded": "this is not a key at all, not even close ok",
	} {
		path := filepath.Join(t.TempDir(), "kek.cred")
		if err := os.WriteFile(path, []byte(body), KEKFileMode); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		if _, err := LoadKEK(path); !errors.Is(err, ErrKEKSize) {
			t.Errorf("LoadKEK(%s) returned %v, want ErrKEKSize", name, err)
		}
	}
}

func TestWriteKEKRejectsWrongSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kek.cred")
	if err := WriteKEK(path, make([]byte, 16)); !errors.Is(err, ErrKEKSize) {
		t.Fatalf("WriteKEK with 16 bytes returned %v, want ErrKEKSize", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("WriteKEK created a file for an invalid key")
	}
}
