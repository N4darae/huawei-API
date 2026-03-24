package hilink

import (
	"errors"
	"testing"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

func TestErrorFixturesMapToSentinels(t *testing.T) {
	cases := []struct {
		file     string
		code     domain.HiLinkCode
		sentinel error
	}{
		{"error_100002.xml", domain.CodeSystemNoSupport, domain.ErrUnsupported},
		{"error_100003.xml", domain.CodeSystemNoRights, domain.ErrNeedLogin},
		{"error_100004.xml", domain.CodeSystemBusy, domain.ErrSystemBusy},
		{"error_100005.xml", domain.CodeFormatError, domain.ErrFormat},
		{"error_108003.xml", domain.CodeLoginAlreadyLogin, domain.ErrLoginRequired},
		{"error_112001.xml", domain.CodeSetNetModeWhenDialup, domain.ErrBusy},
		{"error_125002.xml", domain.CodeSystemCSRF, domain.ErrTokenInvalid},
		{"error_125003.xml", domain.CodeWrongSessionToken, domain.ErrTokenInvalid},
		{"error_125003_compact.xml", domain.CodeWrongSessionToken, domain.ErrTokenInvalid},
	}
	for _, c := range cases {
		body := Fixture(c.file)
		if body == nil {
			t.Fatalf("fixture %s not embedded", c.file)
		}
		err := DecodeError(body, "test/path")
		if err == nil {
			t.Fatalf("%s: no error decoded", c.file)
		}
		if !errors.Is(err, c.sentinel) {
			t.Errorf("%s: not errors.Is %v, got %v", c.file, c.sentinel, err)
		}
		got, ok := domain.HiLinkCodeOf(err)
		if !ok || got != c.code {
			t.Errorf("%s: code = %d %v, want %d", c.file, got, ok, c.code)
		}
	}
}

func TestErrorUnknownCodeStillCarriesCode(t *testing.T) {
	err := DecodeError(Fixture("error_100010.xml"), "test/path")
	if err == nil {
		t.Fatal("expected an error")
	}
	code, ok := domain.HiLinkCodeOf(err)
	if !ok || code != 100010 {
		t.Fatalf("code = %d %v", code, ok)
	}
	if errors.Is(err, domain.ErrTokenInvalid) {
		t.Fatal("unknown code must not match a sentinel it is not")
	}
}

func TestDecodeErrorIgnoresSuccessBody(t *testing.T) {
	for _, name := range []string{"response_ok.xml", "device_information.xml", "sms_sms_list.xml"} {
		if err := DecodeError(Fixture(name), "p"); err != nil {
			t.Errorf("%s decoded as error: %v", name, err)
		}
	}
}

func TestIsTokenErrorCoversAllThreeCodes(t *testing.T) {
	for _, code := range []domain.HiLinkCode{
		domain.CodeWrongToken, domain.CodeSystemCSRF, domain.CodeWrongSessionToken,
	} {
		err := domain.HiLinkError(code, "", "p")
		if !IsTokenError(err) {
			t.Errorf("code %d is not recognised as a token error", code)
		}
	}
	if IsTokenError(domain.HiLinkError(domain.CodeFormatError, "", "p")) {
		t.Error("100005 must not be a token error")
	}
}

func TestEncodeErrorRoundTrips(t *testing.T) {
	body := EncodeError(domain.CodeSetNetModeWhenDialup)
	err := DecodeError(body, "p")
	if !errors.Is(err, domain.ErrBusy) {
		t.Fatalf("round trip failed: %v", err)
	}
}
