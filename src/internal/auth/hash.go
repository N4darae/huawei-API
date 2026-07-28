package auth

import (
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

var (
	ErrHashFormat  = errors.New("auth: password hash is not a supported argon2id string")
	ErrHashVersion = errors.New("auth: password hash was written by a different argon2 version")
)

type Params struct {
	Time    uint32
	Memory  uint32
	Threads uint8
	KeyLen  uint32
	SaltLen uint32
}

func PasswordParams() Params {
	return Params{Time: 2, Memory: 64 * 1024, Threads: 4, KeyLen: 32, SaltLen: 16}
}

func KeyParams() Params {
	return Params{Time: 1, Memory: 8 * 1024, Threads: 2, KeyLen: 32, SaltLen: 16}
}

func (p Params) normalize() Params {
	if p.Time == 0 {
		p.Time = 1
	}
	if p.Memory == 0 {
		p.Memory = 8 * 1024
	}
	if p.Threads == 0 {
		p.Threads = 1
	}
	if p.KeyLen == 0 {
		p.KeyLen = 32
	}
	if p.SaltLen == 0 {
		p.SaltLen = 16
	}
	return p
}

func Hash(secret string, p Params) (string, error) {
	p = p.normalize()
	salt, err := randomBytes(int(p.SaltLen))
	if err != nil {
		return "", err
	}
	sum := argon2.IDKey([]byte(secret), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Time, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(sum),
	), nil
}

func Verify(secret, encoded string) error {
	p, salt, want, err := decodeHash(encoded)
	if err != nil {
		return err
	}
	got := argon2.IDKey([]byte(secret), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrBadCredentials
	}
	return nil
}

func decodeHash(encoded string) (Params, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return Params{}, nil, nil, ErrHashFormat
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return Params{}, nil, nil, ErrHashFormat
	}
	if version != argon2.Version {
		return Params{}, nil, nil, ErrHashVersion
	}

	var p Params
	fields := strings.Split(parts[3], ",")
	if len(fields) != 3 {
		return Params{}, nil, nil, ErrHashFormat
	}
	m, err := parseField(fields[0], "m=")
	if err != nil {
		return Params{}, nil, nil, err
	}
	t, err := parseField(fields[1], "t=")
	if err != nil {
		return Params{}, nil, nil, err
	}
	th, err := parseField(fields[2], "p=")
	if err != nil {
		return Params{}, nil, nil, err
	}
	p.Memory, p.Time, p.Threads = uint32(m), uint32(t), uint8(th)

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return Params{}, nil, nil, ErrHashFormat
	}
	sum, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return Params{}, nil, nil, ErrHashFormat
	}
	p.SaltLen = uint32(len(salt))
	p.KeyLen = uint32(len(sum))
	return p, salt, sum, nil
}

func parseField(in, prefix string) (int, error) {
	if !strings.HasPrefix(in, prefix) {
		return 0, ErrHashFormat
	}
	v, err := strconv.Atoi(strings.TrimPrefix(in, prefix))
	if err != nil || v <= 0 {
		return 0, ErrHashFormat
	}
	return v, nil
}
