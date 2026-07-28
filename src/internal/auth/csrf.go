package auth

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

const (
	CSRFHeader = "X-CSRF-Token"
	CSRFBytes  = 32
)

func NewCSRFToken() (string, error) { return randomToken(CSRFBytes) }

func CheckCSRF(sess Session, got string) error {
	if sess.CSRFToken == "" {
		return ErrBadCSRF
	}
	if subtle.ConstantTimeCompare([]byte(sess.CSRFToken), []byte(strings.TrimSpace(got))) != 1 {
		return ErrBadCSRF
	}
	return nil
}

func CSRFRequired(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

func RequestCSRF(r *http.Request) string { return r.Header.Get(CSRFHeader) }
