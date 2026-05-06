package hilink

import (
	"encoding/xml"
	"errors"
	"strings"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

type errorEnvelope struct {
	XMLName xml.Name `xml:"error"`
	Code    string   `xml:"code"`
	Message string   `xml:"message"`
}

func DecodeError(body []byte, path string) error {
	if rootElement(body) != "error" {
		return nil
	}
	var env errorEnvelope
	if err := xml.Unmarshal(body, &env); err != nil {
		return domain.HiLinkError(0, strings.TrimSpace(string(body)), path)
	}
	code := domain.HiLinkCode(atoi(env.Code))
	return domain.HiLinkError(code, strings.TrimSpace(env.Message), path)
}

func IsTokenError(err error) bool {
	return errors.Is(err, domain.ErrTokenInvalid)
}

func IsLoginError(err error) bool {
	return errors.Is(err, domain.ErrNeedLogin) || errors.Is(err, domain.ErrLoginRequired)
}

func EncodeError(code domain.HiLinkCode) []byte {
	return []byte(XMLProlog + "\n<error>\n<code>" + itoa(int(code)) + "</code>\n<message></message>\n</error>\n")
}
