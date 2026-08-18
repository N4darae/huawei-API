package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/n4darae/huawei-API/src/internal/auth"
	"github.com/n4darae/huawei-API/src/internal/config"
	"github.com/n4darae/huawei-API/src/internal/devops"
	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/eventbus"
	"github.com/n4darae/huawei-API/src/internal/logging"
	"github.com/n4darae/huawei-API/src/internal/proxysup"
	"github.com/n4darae/huawei-API/src/internal/ratelimit"
	"github.com/n4darae/huawei-API/src/internal/reconcile"
	"github.com/n4darae/huawei-API/src/internal/rotate"
	"github.com/n4darae/huawei-API/src/internal/store"
)

const (
	MaxBodyBytes    = 1 << 20
	WaitCap         = 90 * time.Second
	DefaultPingWait = 25 * time.Second
)

type Waiter interface {
	Wait(ctx context.Context, operationID string) (domain.Operation, error)
}

type Observer interface {
	Snapshot() reconcile.ObservedState
}

type Deps struct {
	NodeID            string
	Version           string
	Repos             store.Repos
	Rotator           rotate.Rotator
	Waiter            Waiter
	DevOps            devops.Ops
	Bus               eventbus.Bus
	Observer          Observer
	Sessions          *auth.Sessions
	Keys              *auth.Keys
	Lockout           *auth.Lockout
	Limiter           *ratelimit.Limiter
	Clock             domain.Clock
	Log               *slog.Logger
	MinRotateInterval time.Duration
	LinkCooldown      time.Duration
	SecureCookies     bool
	PingInterval      time.Duration
}

type API struct {
	deps Deps
	mw   auth.Middleware
	link *linkGuard
}

var (
	ErrNoRepos    = errors.New("httpapi: a store.Repos is required")
	ErrNoSessions = errors.New("httpapi: an auth.Sessions is required")
	ErrNoKeys     = errors.New("httpapi: an auth.Keys is required")
)

func New(d Deps) (*API, error) {
	if d.Repos == nil {
		return nil, ErrNoRepos
	}
	if d.Sessions == nil {
		return nil, ErrNoSessions
	}
	if d.Keys == nil {
		return nil, ErrNoKeys
	}
	if d.Clock == nil {
		d.Clock = domain.SystemClock()
	}
	if d.Log == nil {
		d.Log = logging.New(os.Stderr, "info")
	}
	if d.Limiter == nil {
		d.Limiter = ratelimit.New(ratelimit.DefaultLimit(), d.Clock.Now)
	}
	if d.Lockout == nil {
		return nil, errors.New("httpapi: an auth.Lockout is required")
	}
	if d.MinRotateInterval <= 0 {
		d.MinRotateInterval = config.Default().Carrier.MinRotateInterval
	}
	if d.LinkCooldown <= 0 {
		d.LinkCooldown = d.MinRotateInterval
	}
	if d.PingInterval <= 0 {
		d.PingInterval = DefaultPingWait
	}

	a := &API{deps: d, link: newLinkGuard(d.LinkCooldown, d.Clock.Now)}
	a.mw = auth.Middleware{
		Sessions:   d.Sessions,
		Keys:       d.Keys,
		Limiter:    d.Limiter,
		Deny:       a.deny,
		CSRFExempt: csrfExempt,
	}
	return a, nil
}

func csrfExempt(r *http.Request) bool {
	return strings.HasSuffix(r.URL.Path, "/auth/logout")
}

func (a *API) deny(w http.ResponseWriter, r *http.Request, d auth.Denial) {
	writeError(w, r, apiError{
		Status:     d.Status,
		Code:       d.Code,
		Message:    d.Message,
		RetryAfter: d.RetryAfter,
	})
}

func (a *API) now() time.Time { return a.deps.Clock.Now() }

func (a *API) nowMS() int64 { return domain.UnixMillis(a.now()) }

func (a *API) snapshot() reconcile.ObservedState {
	if a.deps.Observer == nil {
		return reconcile.ObservedState{
			Devices:     map[domain.Slot]reconcile.DeviceObservation{},
			ProxyStatus: map[domain.Slot]proxysup.Status{},
		}
	}
	return a.deps.Observer.Snapshot()
}

func (a *API) Mount(r chi.Router) {
	r.Route(APIBase+"/auth", func(r chi.Router) {
		r.Post("/login", a.handleLogin)
		r.Group(func(r chi.Router) {
			r.Use(a.mw.RequireSession)
			r.Post("/logout", a.handleLogout)
			r.Get("/session", a.handleSession)
		})
	})

	r.Group(func(r chi.Router) {
		r.Use(a.mw.RequireSession)

		r.Get(APIBase+"/proxies", a.listProxies)
		r.Get(APIBase+"/proxies/export", a.exportProxies)
		r.Get(APIBase+"/proxies/{proxy_id}", a.getProxy)
		r.Post(APIBase+"/proxies/{proxy_id}/rotate", a.rotateProxyAdmin)
		r.Post(APIBase+"/proxies/{proxy_id}/auth", a.setProxyAuth)
		r.Get(APIBase+"/proxies/{proxy_id}/auth-ips", a.listProxyAuthIPs)
		r.Post(APIBase+"/proxies/{proxy_id}/auth-ips", a.addProxyAuthIP)
		r.Delete(APIBase+"/proxies/{proxy_id}/auth-ips", a.deleteProxyAuthIP)
		r.Post(APIBase+"/proxies/{proxy_id}/ports", a.setProxyPorts)
		r.Post(APIBase+"/proxies/{proxy_id}/enable", a.setProxyEnabled)
		r.Post(APIBase+"/proxies/{proxy_id}/customer", a.assignProxyCustomer)
		r.Post(APIBase+"/proxies/{proxy_id}/selftest", a.selftestProxy)

		r.Get(APIBase+"/slots", a.listSlots)
		r.Get(APIBase+"/dongles", a.listDongles)
		r.Get(APIBase+"/dongles/{dongle_id}", a.getDongle)
		r.Patch(APIBase+"/dongles/{dongle_id}", a.patchDongle)
		r.Post(APIBase+"/dongles/{dongle_id}/reboot", a.rebootDongle)
		r.Post(APIBase+"/dongles/{dongle_id}/netmode", a.setDongleNetMode)
		r.Post(APIBase+"/dongles/{dongle_id}/lanip", a.setDongleLanIP)
		r.Get(APIBase+"/dongles/{dongle_id}/sms", a.listSms)
		r.Post(APIBase+"/dongles/{dongle_id}/sms/send", a.sendSms)
		r.Post(APIBase+"/dongles/{dongle_id}/sms/delete", a.deleteSms)
		r.Post(APIBase+"/dongles/{dongle_id}/sms/read", a.markSmsRead)

		r.Get(APIBase+"/operations", a.listOperations)
		r.Get(APIBase+"/operations/{op_id}", a.getOperation)
		r.Get(APIBase+"/rotations", a.listRotations)

		r.Get(APIBase+"/customers", a.listCustomers)
		r.Post(APIBase+"/customers", a.createCustomer)
		r.Patch(APIBase+"/customers/{customer_id}", a.patchCustomer)

		r.Get(APIBase+"/keys", a.listApiKeys)
		r.Post(APIBase+"/keys", a.createApiKey)
		r.Delete(APIBase+"/keys/{key_id}", a.revokeApiKey)
		r.Post(APIBase+"/keys/{key_id}/link-tokens", a.createLinkToken)
		r.Delete(APIBase+"/link-tokens/{token_id}", a.revokeLinkToken)

		r.Get(EventPath, a.events)
	})

	r.Group(func(r chi.Router) {
		r.Use(a.mw.RequireKey(auth.ScopeRotate))
		r.Post(APIBase+"/rotate/{proxy_id}", a.rotateProxyCustomer)
	})
	r.Group(func(r chi.Router) {
		r.Use(a.mw.RequireKey(auth.ScopeStatus))
		r.Get(APIBase+"/status/{proxy_id}", a.customerStatus)
	})

	r.Get(LinkBase+"/{link_token}", a.linkConfirmPage)
	r.Post(LinkBase+"/{link_token}", a.linkRotate)
}

func (a *API) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := decodeBody(w, r, &req); err != nil {
		writeError(w, r, translate(err))
		return
	}

	subject := strings.ToLower(strings.TrimSpace(req.Username)) + "@" + peerIP(r)
	wait, err := a.deps.Lockout.Check(r.Context(), subject)
	if err != nil {
		writeError(w, r, translate(err))
		return
	}
	if wait > 0 {
		a.deps.Log.Warn("login refused while locked out", slog.String("ip", clientIP(r)))
		writeError(w, r, apiError{
			Status:     http.StatusTooManyRequests,
			Code:       CodeRateLimited,
			Message:    "too many failed sign in attempts, try again later",
			RetryAfter: wait,
		})
		return
	}

	if err := a.deps.Sessions.Authenticate(r.Context(), req.Username, req.Password); err != nil {
		if !errors.Is(err, auth.ErrBadCredentials) {
			writeError(w, r, translate(err))
			return
		}
		locked, lerr := a.deps.Lockout.Fail(r.Context(), subject)
		if lerr != nil {
			a.deps.Log.Error("cannot record a failed sign in", slog.String("error", lerr.Error()))
		}
		if locked > 0 {
			writeError(w, r, apiError{
				Status:     http.StatusTooManyRequests,
				Code:       CodeRateLimited,
				Message:    "too many failed sign in attempts, try again later",
				RetryAfter: locked,
			})
			return
		}
		writeError(w, r, fail(http.StatusUnauthorized, CodeInvalidCreds, "username or password is wrong"))
		return
	}

	if err := a.deps.Lockout.Reset(r.Context(), subject); err != nil {
		a.deps.Log.Error("cannot clear the lockout counter", slog.String("error", err.Error()))
	}

	_, secret, err := a.deps.Sessions.Issue(r.Context(), req.Username)
	if err != nil {
		writeError(w, r, translate(err))
		return
	}
	http.SetCookie(w, a.deps.Sessions.Cookie(secret, a.deps.SecureCookies))
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := a.deps.Sessions.Revoke(r.Context(), auth.CookieValue(r)); err != nil {
		writeError(w, r, translate(err))
		return
	}
	http.SetCookie(w, a.deps.Sessions.ClearCookie(a.deps.SecureCookies))
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleSession(w http.ResponseWriter, r *http.Request) {
	sess, ok := auth.SessionFrom(r.Context())
	if !ok {
		writeError(w, r, fail(http.StatusUnauthorized, CodeUnauthorized, "sign in to use the panel"))
		return
	}
	WriteJSON(w, http.StatusOK, SessionBody{
		Username:  sess.Username,
		ExpiresAt: sess.ExpiresAt,
		CSRFToken: sess.CSRFToken,
	})
}

func decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	body := http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	raw, err := io.ReadAll(body)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return fail(http.StatusRequestEntityTooLarge, CodePayloadTooLarge,
				"request body exceeds the 1 MiB limit")
		}
		return domain.Wrap(domain.ErrInvalid, "httpapi: request body could not be read")
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	if err := dec.Decode(dst); err != nil {
		return domain.Wrap(domain.ErrInvalid, "httpapi: request body is not valid json")
	}
	return nil
}

func peerIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if addr, ok := r.Context().Value(peerAddrKey{}).(string); ok && addr != "" {
		return parseIP(addr)
	}
	return clientIP(r)
}

func clientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	return parseIP(r.RemoteAddr)
}

func parseIP(remoteAddr string) string {
	if remoteAddr == "" {
		return ""
	}
	if ap, err := netip.ParseAddrPort(remoteAddr); err == nil {
		return ap.Addr().String()
	}
	if a, err := netip.ParseAddr(remoteAddr); err == nil {
		return a.String()
	}
	host, _, found := strings.Cut(remoteAddr, ":")
	if found {
		return host
	}
	return remoteAddr
}

func queryInt(r *http.Request, name string) (int, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return 0, false
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return n, true
}

func queryBool(r *http.Request, name string) bool {
	raw := strings.TrimSpace(strings.ToLower(r.URL.Query().Get(name)))
	return raw == "1" || raw == "true" || raw == "yes"
}
