package domain

import (
	"errors"
	"testing"
)

func TestHiLinkCodeMapping(t *testing.T) {
	cases := []struct {
		code HiLinkCode
		want error
	}{
		{100001, ErrHiLinkUnknown},
		{100002, ErrUnsupported},
		{100003, ErrNeedLogin},
		{100004, ErrSystemBusy},
		{100005, ErrFormat},
		{108001, ErrLoginRequired},
		{108003, ErrLoginRequired},
		{108007, ErrLoginRequired},
		{112001, ErrBusy},
		{120001, ErrVoiceBusy},
		{125001, ErrTokenInvalid},
		{125002, ErrTokenInvalid},
		{125003, ErrTokenInvalid},
	}
	for _, c := range cases {
		got, ok := HiLinkSentinel(c.code)
		if !ok {
			t.Errorf("code %d has no sentinel", c.code)
			continue
		}
		if !errors.Is(got, c.want) {
			t.Errorf("code %d mapped to %v, want %v", c.code, got, c.want)
		}
		err := HiLinkError(c.code, "", "/api/device/information")
		if !errors.Is(err, c.want) {
			t.Errorf("APIError(%d) does not unwrap to %v", c.code, c.want)
		}
	}
}

func TestNeedLoginAndUnsupportedAreDistinct(t *testing.T) {
	if errors.Is(ErrNeedLogin, ErrUnsupported) || errors.Is(ErrUnsupported, ErrNeedLogin) {
		t.Fatal("ErrNeedLogin and ErrUnsupported must be distinct sentinels")
	}
	needLogin, _ := HiLinkSentinel(CodeSystemNoRights)
	unsupported, _ := HiLinkSentinel(CodeSystemNoSupport)
	if needLogin == unsupported {
		t.Fatal("100003 and 100002 must not share a sentinel")
	}
	if CodeSystemNoRights != 100003 {
		t.Fatalf("login-required code is %d, must be 100003", CodeSystemNoRights)
	}
	if CodeSystemNoSupport != 100002 {
		t.Fatalf("no-support code is %d, must be 100002", CodeSystemNoSupport)
	}
	if CodeFormatError != 100005 {
		t.Fatalf("format-error code is %d, must be 100005", CodeFormatError)
	}
}

func TestCode100006DoesNotExist(t *testing.T) {
	if _, ok := HiLinkSentinel(HiLinkCode(100006)); ok {
		t.Fatal("100006 is not a Huawei response code and must not map to a sentinel")
	}
	for _, c := range KnownHiLinkCodes() {
		if c == 100006 {
			t.Fatal("100006 leaked into the known code table")
		}
	}
}

func TestAPIErrorCarriesCode(t *testing.T) {
	err := HiLinkError(CodeSetNetModeWhenDialup, "set net mode while dialup", "/api/net/net-mode")
	code, ok := HiLinkCodeOf(err)
	if !ok || code != 112001 {
		t.Fatalf("HiLinkCodeOf = %d %v", code, ok)
	}
	if !errors.Is(err, ErrBusy) {
		t.Fatal("112001 must satisfy errors.Is(err, ErrBusy)")
	}
	if errors.Is(err, ErrNeedLogin) {
		t.Fatal("112001 must not satisfy errors.Is(err, ErrNeedLogin)")
	}
}

func TestTriggerEnumHasNoSchedule(t *testing.T) {
	for _, tr := range AllTriggers() {
		if string(tr) == "schedule" {
			t.Fatal("'schedule' was cut from the trigger enum")
		}
	}
	if len(AllTriggers()) != 3 {
		t.Fatalf("trigger enum has %d members, want 3", len(AllTriggers()))
	}
}

func TestRotateStepContract(t *testing.T) {
	want := []string{"precheck", "fence", "data_off", "hold", "data_on", "wait_connect", "unfence", "verify", "done"}
	got := RotateSteps()
	if len(got) != len(want) {
		t.Fatalf("rotate step sequence has %d steps, want %d", len(got), len(want))
	}
	for i := range want {
		if string(got[i]) != want[i] {
			t.Errorf("step %d = %q, want %q", i, got[i], want[i])
		}
	}
}
