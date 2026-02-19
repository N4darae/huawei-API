package secrets

import "errors"

const (
	KEKSize      = 32
	NonceSize    = 24
	BackupMaxAge = 7
)

type Sealer interface {
	Seal(plaintext []byte) ([]byte, error)
	Open(ciphertext []byte) ([]byte, error)
}

var (
	ErrKEKMissing  = errors.New("secrets: kek credential is missing; run dongled restore-kek")
	ErrKEKSize     = errors.New("secrets: kek must be 32 bytes")
	ErrOpenFailed  = errors.New("secrets: ciphertext failed authentication")
	ErrNotUnsealed = errors.New("secrets: sealer is not initialised")
)
