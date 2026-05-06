package secrets

import (
	"crypto/cipher"
	"crypto/rand"
	"fmt"

	"golang.org/x/crypto/chacha20poly1305"
)

type box struct {
	aead cipher.AEAD
}

func NewSealer(kek []byte) (Sealer, error) {
	if len(kek) != KEKSize {
		return nil, fmt.Errorf("%w: got %d bytes", ErrKEKSize, len(kek))
	}
	aead, err := chacha20poly1305.NewX(kek)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKEKSize, err)
	}
	if aead.NonceSize() != NonceSize {
		return nil, fmt.Errorf("%w: aead nonce is %d bytes, contract requires %d", ErrKEKSize, aead.NonceSize(), NonceSize)
	}
	return &box{aead: aead}, nil
}

func (b *box) Seal(plaintext []byte) ([]byte, error) {
	if b == nil || b.aead == nil {
		return nil, ErrNotUnsealed
	}
	nonce := make([]byte, NonceSize, NonceSize+len(plaintext)+b.aead.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, plaintext, nil), nil
}

func (b *box) Open(ciphertext []byte) ([]byte, error) {
	if b == nil || b.aead == nil {
		return nil, ErrNotUnsealed
	}
	min := NonceSize + b.aead.Overhead()
	if len(ciphertext) < min {
		return nil, fmt.Errorf("%w: ciphertext is %d bytes, minimum is %d", ErrOpenFailed, len(ciphertext), min)
	}
	out, err := b.aead.Open(nil, ciphertext[:NonceSize], ciphertext[NonceSize:], nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOpenFailed, err)
	}
	return out, nil
}
