package domain

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
)

var (
	ErrNotFound       = errors.New("dongled: not found")
	ErrConflict       = errors.New("dongled: conflict")
	ErrInvalid        = errors.New("dongled: invalid argument")
	ErrUnauthorized   = errors.New("dongled: unauthorized")
	ErrForbidden      = errors.New("dongled: forbidden")
	ErrRateLimited    = errors.New("dongled: rate limited")
	ErrExpired        = errors.New("dongled: expired")
	ErrNotImplemented = errors.New("dongled: not implemented")
	ErrOpInProgress   = errors.New("dongled: operation already in progress")
	ErrSlotOccupied   = errors.New("dongled: slot already holds a dongle")
	ErrNoFreeSlot     = errors.New("dongled: no free slot")
	ErrSimLocked      = errors.New("dongled: sim locked, PIN or PUK required")
	ErrDegraded       = errors.New("dongled: degraded, refusing to act")
)

type HiLinkCode int

const (
	CodeSystemUnknown   HiLinkCode = 100001
	CodeSystemNoSupport HiLinkCode = 100002
	CodeSystemNoRights  HiLinkCode = 100003
	CodeSystemBusy      HiLinkCode = 100004
	CodeFormatError     HiLinkCode = 100005

	CodeSetNetModeWhenDialup HiLinkCode = 112001

	CodeVoiceBusy HiLinkCode = 120001

	CodeWrongToken        HiLinkCode = 125001
	CodeSystemCSRF        HiLinkCode = 125002
	CodeWrongSessionToken HiLinkCode = 125003

	CodeLoginUsernameWrong      HiLinkCode = 108001
	CodeLoginPasswordWrong      HiLinkCode = 108002
	CodeLoginAlreadyLogin       HiLinkCode = 108003
	CodeLoginUsernamePwdWrong   HiLinkCode = 108006
	CodeLoginUsernamePwdOverrun HiLinkCode = 108007

	CodeLoginErrorMin HiLinkCode = 108001
	CodeLoginErrorMax HiLinkCode = 108007
)

var (
	ErrHiLinkUnknown = errors.New("hilink: 100001 system unknown")
	ErrUnsupported   = errors.New("hilink: 100002 not supported")
	ErrNeedLogin     = errors.New("hilink: 100003 no rights, login required")
	ErrSystemBusy    = errors.New("hilink: 100004 system busy")
	ErrFormat        = errors.New("hilink: 100005 format error")
	ErrBusy          = errors.New("hilink: 112001 busy, dialup in progress")
	ErrVoiceBusy     = errors.New("hilink: 120001 voice busy")
	ErrTokenInvalid  = errors.New("hilink: 125001/125002/125003 token invalid")
	ErrLoginRequired = errors.New("hilink: 108001-108007 password protected, unsupported in v1")
	ErrUnreachable   = errors.New("hilink: unreachable")
)

var hilinkSentinels = map[HiLinkCode]error{
	CodeSystemUnknown:        ErrHiLinkUnknown,
	CodeSystemNoSupport:      ErrUnsupported,
	CodeSystemNoRights:       ErrNeedLogin,
	CodeSystemBusy:           ErrSystemBusy,
	CodeFormatError:          ErrFormat,
	CodeSetNetModeWhenDialup: ErrBusy,
	CodeVoiceBusy:            ErrVoiceBusy,
	CodeWrongToken:           ErrTokenInvalid,
	CodeSystemCSRF:           ErrTokenInvalid,
	CodeWrongSessionToken:    ErrTokenInvalid,
}

func HiLinkSentinel(code HiLinkCode) (error, bool) {
	if code >= CodeLoginErrorMin && code <= CodeLoginErrorMax {
		return ErrLoginRequired, true
	}
	e, ok := hilinkSentinels[code]
	return e, ok
}

func KnownHiLinkCodes() []HiLinkCode {
	out := make([]HiLinkCode, 0, len(hilinkSentinels)+5)
	for c := range hilinkSentinels {
		out = append(out, c)
	}
	out = append(out,
		CodeLoginUsernameWrong,
		CodeLoginPasswordWrong,
		CodeLoginAlreadyLogin,
		CodeLoginUsernamePwdWrong,
		CodeLoginUsernamePwdOverrun,
	)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

type APIError struct {
	Code HiLinkCode
	Msg  string
	Path string
}

func (e *APIError) Error() string {
	s := "hilink: error " + strconv.Itoa(int(e.Code))
	if e.Path != "" {
		s += " on " + e.Path
	}
	if e.Msg != "" {
		s += ": " + e.Msg
	}
	return s
}

func (e *APIError) Unwrap() error {
	if s, ok := HiLinkSentinel(e.Code); ok {
		return s
	}
	return nil
}

func (e *APIError) Is(target error) bool {
	other, ok := target.(*APIError)
	if !ok {
		return false
	}
	return other.Code == e.Code
}

func NewAPIError(code HiLinkCode, msg, path string) *APIError {
	return &APIError{Code: code, Msg: msg, Path: path}
}

func HiLinkError(code HiLinkCode, msg, path string) error {
	return NewAPIError(code, msg, path)
}

func HiLinkCodeOf(err error) (HiLinkCode, bool) {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Code, true
	}
	return 0, false
}

func Wrap(err error, format string, args ...any) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf(format+": %w", append(args, err)...)
}
