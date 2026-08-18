package httpapi

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/n4darae/huawei-API/src/internal/auth"
	"github.com/n4darae/huawei-API/src/internal/devops"
	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/rotate"
)

const (
	CodeUnauthorized      = "unauthorized"
	CodeInvalidCreds      = "invalid_credentials"
	CodeCSRFInvalid       = "csrf_invalid"
	CodeForbidden         = "forbidden"
	CodeScopeMissing      = "scope_missing"
	CodeKeyRevoked        = "key_revoked"
	CodeNotFound          = "not_found"
	CodeInvalidRequest    = "invalid_request"
	CodePayloadTooLarge   = "payload_too_large"
	CodeOpInProgress      = "op_in_progress"
	CodeConflict          = "conflict"
	CodeRateLimited       = "rate_limited"
	CodeSimPinRequired    = "sim_pin_required"
	CodeDeviceUnreachable = "device_unreachable"
	CodeDegraded          = "degraded"
	CodeProxyExpired      = "proxy_expired"
	CodeNotImplemented    = "not_implemented"
	CodeMethodNotAllowed  = "method_not_allowed"
	CodeTimeout           = "timeout"
	CodeInternal          = "internal"
)

func AllErrorCodes() []string {
	return []string{
		CodeUnauthorized, CodeInvalidCreds, CodeCSRFInvalid, CodeForbidden, CodeScopeMissing,
		CodeKeyRevoked, CodeNotFound, CodeInvalidRequest, CodePayloadTooLarge, CodeOpInProgress, CodeConflict,
		CodeRateLimited, CodeSimPinRequired, CodeDeviceUnreachable, CodeDegraded, CodeProxyExpired,
		CodeNotImplemented, CodeMethodNotAllowed, CodeTimeout, CodeInternal,
	}
}

type ErrorBody struct {
	Error      string `json:"error"`
	Message    string `json:"message"`
	RequestID  string `json:"request_id,omitempty"`
	RetryAfter int    `json:"retry_after,omitempty"`
}

type OpInProgressBody struct {
	Error       string `json:"error"`
	Message     string `json:"message,omitempty"`
	OperationID string `json:"operation_id"`
	PollURL     string `json:"poll_url,omitempty"`
	RequestID   string `json:"request_id,omitempty"`
}

type apiError struct {
	Status      int
	Code        string
	Message     string
	RetryAfter  time.Duration
	OperationID string
}

func (e apiError) Error() string { return e.Code + ": " + e.Message }

func fail(status int, code, message string) apiError {
	return apiError{Status: status, Code: code, Message: message}
}

func requestID(r *http.Request) string {
	if r == nil {
		return ""
	}
	return middleware.GetReqID(r.Context())
}

func secondsFor(d time.Duration) int {
	if d <= 0 {
		return 1
	}
	return int(math.Ceil(d.Seconds()))
}

func writeError(w http.ResponseWriter, r *http.Request, e apiError) {
	if e.Status == 0 {
		e.Status = http.StatusInternalServerError
	}
	if e.Code == "" {
		e.Code = CodeInternal
	}

	retry := 0
	if e.RetryAfter > 0 {
		retry = secondsFor(e.RetryAfter)
		w.Header().Set("Retry-After", strconv.Itoa(retry))
	}

	if e.OperationID != "" {
		WriteJSON(w, e.Status, OpInProgressBody{
			Error:       e.Code,
			Message:     e.Message,
			OperationID: e.OperationID,
			PollURL:     operationURL(e.OperationID),
			RequestID:   requestID(r),
		})
		return
	}

	WriteJSON(w, e.Status, ErrorBody{
		Error:      e.Code,
		Message:    e.Message,
		RequestID:  requestID(r),
		RetryAfter: retry,
	})
}

func operationURL(id string) string {
	if id == "" {
		return ""
	}
	return APIBase + "/operations/" + id
}

type activeOperation interface{ ActiveOperationID() string }

func translate(err error) apiError {
	if err == nil {
		return apiError{}
	}

	var known apiError
	if errors.As(err, &known) {
		return known
	}

	var live activeOperation
	if errors.As(err, &live) || errors.Is(err, domain.ErrOpInProgress) {
		id := ""
		if live != nil {
			id = live.ActiveOperationID()
		}
		return apiError{
			Status:      http.StatusConflict,
			Code:        CodeOpInProgress,
			Message:     "another operation is already running on this subject",
			OperationID: id,
		}
	}

	var tooSoon *rotate.TooSoonError
	if errors.As(err, &tooSoon) {
		return apiError{
			Status:     http.StatusTooManyRequests,
			Code:       CodeRateLimited,
			Message:    "the minimum interval between rotations of this proxy has not elapsed",
			RetryAfter: tooSoon.RetryAfter,
		}
	}

	switch {
	case errors.Is(err, domain.ErrSimLocked):
		return fail(http.StatusConflict, CodeSimPinRequired, "the SIM is locked and needs its PIN or PUK entered on the dongle")
	case errors.Is(err, rotate.ErrMinInterval), errors.Is(err, domain.ErrRateLimited):
		return apiError{
			Status:     http.StatusTooManyRequests,
			Code:       CodeRateLimited,
			Message:    "rate limited",
			RetryAfter: time.Minute,
		}
	case errors.Is(err, domain.ErrNotFound):
		return fail(http.StatusNotFound, CodeNotFound, err.Error())
	case errors.Is(err, domain.ErrInvalid):
		return fail(http.StatusBadRequest, CodeInvalidRequest, err.Error())
	case errors.Is(err, domain.ErrUnauthorized):
		return fail(http.StatusUnauthorized, CodeUnauthorized, "authentication is required")
	case errors.Is(err, domain.ErrForbidden):
		return fail(http.StatusForbidden, CodeForbidden, err.Error())
	case errors.Is(err, domain.ErrExpired):
		return fail(http.StatusForbidden, CodeProxyExpired, err.Error())
	case errors.Is(err, domain.ErrDegraded):
		return fail(http.StatusServiceUnavailable, CodeDegraded, err.Error())
	case errors.Is(err, domain.ErrUnreachable):
		return fail(http.StatusBadGateway, CodeDeviceUnreachable, "the dongle did not answer")
	case errors.Is(err, domain.ErrNotImplemented), errors.Is(err, devops.ErrNoNetcfg):
		return fail(http.StatusNotImplemented, CodeNotImplemented, err.Error())
	case errors.Is(err, devops.ErrLanIPUnsupported):
		return fail(http.StatusConflict, CodeConflict, err.Error())
	case errors.Is(err, domain.ErrConflict):
		return fail(http.StatusConflict, CodeConflict, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return fail(http.StatusGatewayTimeout, CodeTimeout, "the operation did not finish in time")
	case errors.Is(err, context.Canceled):
		return fail(http.StatusRequestTimeout, CodeTimeout, "the request was cancelled")
	case errors.Is(err, auth.ErrBadCredentials):
		return fail(http.StatusUnauthorized, CodeInvalidCreds, "username or password is wrong")
	case errors.Is(err, auth.ErrNoSession), errors.Is(err, auth.ErrSessionExpired):
		return fail(http.StatusUnauthorized, CodeUnauthorized, "sign in to use the panel")
	case errors.Is(err, auth.ErrBadCSRF):
		return fail(http.StatusForbidden, CodeCSRFInvalid, "missing or stale "+auth.CSRFHeader)
	case errors.Is(err, auth.ErrKeyRevoked):
		return fail(http.StatusUnauthorized, CodeKeyRevoked, "this api key was revoked")
	case errors.Is(err, auth.ErrBadKey):
		return fail(http.StatusUnauthorized, CodeUnauthorized, "api key is not valid")
	default:
		return fail(http.StatusInternalServerError, CodeInternal, "the panel could not complete this request")
	}
}
