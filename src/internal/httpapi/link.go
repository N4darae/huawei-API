package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/n4darae/huawei-API/src/internal/auth"
	"github.com/n4darae/huawei-API/src/internal/domain"
)

type linkGuard struct {
	cooldown time.Duration
	now      func() time.Time

	mu   sync.Mutex
	last map[string]time.Time
}

func newLinkGuard(cooldown time.Duration, now func() time.Time) *linkGuard {
	if now == nil {
		now = time.Now
	}
	return &linkGuard{cooldown: cooldown, now: now, last: map[string]time.Time{}}
}

func (g *linkGuard) take(tokenID string) time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := g.now()
	if at, ok := g.last[tokenID]; ok {
		if elapsed := now.Sub(at); elapsed < g.cooldown {
			return g.cooldown - elapsed
		}
	}
	g.last[tokenID] = now
	return 0
}

var linkPage = template.Must(template.New("link").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>Rotate proxy {{.ProxyID}}</title>
<style>
:root { color-scheme: light dark; }
body { margin:0; min-height:100vh; display:grid; place-items:center;
       font:16px/1.5 ui-sans-serif,system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;
       background:Canvas; color:CanvasText; }
main { width:min(28rem,92vw); padding:2rem; border:1px solid color-mix(in srgb, CanvasText 18%, transparent);
       border-radius:14px; }
h1 { font-size:1.25rem; margin:0 0 .25rem; }
p { margin:.5rem 0; }
dl { display:grid; grid-template-columns:auto 1fr; gap:.25rem 1rem; margin:1.25rem 0; }
dt { color:color-mix(in srgb, CanvasText 60%, transparent); }
dd { margin:0; font-variant-numeric:tabular-nums; }
button { width:100%; padding:.75rem 1rem; font:inherit; font-weight:600; cursor:pointer;
         border:0; border-radius:10px; background:AccentColor; color:AccentColorText; }
.note { font-size:.875rem; color:color-mix(in srgb, CanvasText 60%, transparent); }
.bad { color:#b3261e; }
</style>
</head>
<body>
<main>
<h1>Rotate this proxy</h1>
{{if .Error}}
<p class="bad">{{.Error}}</p>
{{else}}
<p>This gives proxy <strong>{{.ProxyID}}</strong> a new public IP address. Existing connections drop.</p>
<dl>
  <dt>Proxy</dt><dd>{{.ProxyID}}</dd>
  <dt>Current IP</dt><dd>{{if .WanIP}}{{.WanIP}}{{else}}unknown{{end}}</dd>
  <dt>Minimum interval</dt><dd>{{.MinIntervalS}}s</dd>
</dl>
<form method="post">
  <button type="submit">Rotate now</button>
</form>
<p class="note">Nothing happens until you press the button. Opening this page, or a mail or antivirus
scanner opening it for you, never changes the address.</p>
{{end}}
{{if .Result}}
<dl>
  <dt>Result</dt><dd>{{.Result}}</dd>
  <dt>Old IP</dt><dd>{{.OldIP}}</dd>
  <dt>New IP</dt><dd>{{.NewIP}}</dd>
</dl>
{{end}}
</main>
</body>
</html>
`))

type linkPageData struct {
	ProxyID      string
	WanIP        string
	MinIntervalS int
	Error        string
	Result       string
	OldIP        string
	NewIP        string
}

func (a *API) linkTarget(r *http.Request) (auth.Key, auth.LinkToken, string, error) {
	secret := chi.URLParam(r, "link_token")
	token, key, err := a.deps.Keys.AuthenticateLink(r.Context(), secret)
	if err != nil {
		return auth.Key{}, auth.LinkToken{}, "", err
	}
	if !key.HasScope(auth.ScopeRotate) {
		return auth.Key{}, auth.LinkToken{}, "", domain.Wrap(domain.ErrForbidden, "httpapi: this link may not rotate")
	}
	if len(key.ProxyIDs) != 1 {
		return auth.Key{}, auth.LinkToken{}, "",
			domain.Wrap(domain.ErrInvalid, "httpapi: a rotate link needs an api key scoped to exactly one proxy, this one covers %d", len(key.ProxyIDs))
	}
	return key, token, key.ProxyIDs[0], nil
}

func wantsHTML(r *http.Request) bool {
	accept := strings.ToLower(r.Header.Get("Accept"))
	if strings.Contains(accept, "application/json") {
		return false
	}
	return strings.Contains(accept, "text/html") || accept == "" || strings.Contains(accept, "*/*")
}

func (a *API) linkConfirmPage(w http.ResponseWriter, r *http.Request) {
	_, _, proxyID, err := a.linkTarget(r)
	if err != nil {
		a.linkFailure(w, r, err)
		return
	}

	data := linkPageData{ProxyID: proxyID, MinIntervalS: int(a.deps.MinRotateInterval / time.Second)}
	if px, err := a.deps.Repos.Proxies().Get(r.Context(), proxyID); err == nil {
		if row, err := a.deps.Repos.Slots().Get(r.Context(), px.SlotID); err == nil {
			data.WanIP = addrText(a.snapshot().Devices[row.Slot].WanIP)
		}
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	linkPage.Execute(w, data)
}

func (a *API) linkRotate(w http.ResponseWriter, r *http.Request) {
	key, token, proxyID, err := a.linkTarget(r)
	if err != nil {
		a.linkFailure(w, r, err)
		return
	}

	if wait := a.link.take(token.ID); wait > 0 {
		a.linkFailure(w, r, apiError{
			Status:     http.StatusTooManyRequests,
			Code:       CodeRateLimited,
			Message:    "this link was used moments ago, wait for the cooldown before rotating again",
			RetryAfter: wait,
		})
		return
	}

	d := a.deps.Limiter.Allow(key.ID)
	auth.WriteRateLimitHeaders(w, d)
	if !d.Allowed {
		a.linkFailure(w, r, apiError{
			Status:     http.StatusTooManyRequests,
			Code:       CodeRateLimited,
			Message:    "api key rate limit exceeded",
			RetryAfter: d.RetryAfter,
		})
		return
	}
	a.deps.Keys.TouchLinkToken(r.Context(), token.ID)

	if !wantsHTML(r) {
		a.customerRotate(w, r, proxyID, key.ID, queryBool(r, "wait"))
		return
	}
	a.linkRotateHTML(w, r, proxyID, key.ID)
}

func (a *API) linkRotateHTML(w http.ResponseWriter, r *http.Request, proxyID, actorID string) {
	rec := newRecorder()
	a.customerRotate(rec, r, proxyID, actorID, true)

	data := linkPageData{ProxyID: proxyID, MinIntervalS: int(a.deps.MinRotateInterval / time.Second)}
	res := rec.rotateResult()
	switch {
	case res != nil:
		data.Result = res.Result
		data.OldIP = res.OldIP
		data.NewIP = res.NewIP
		if res.Result != string(domain.RotationChanged) && res.Result != string(domain.RotationUnchanged) {
			data.Error = "the rotation finished without a new address"
		}
	case rec.status >= 400:
		data.Error = rec.errorMessage()
	default:
		data.Result = "started"
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(rec.status)
	linkPage.Execute(w, data)
}

func (a *API) linkFailure(w http.ResponseWriter, r *http.Request, err error) {
	e := translate(err)
	if errors.Is(err, auth.ErrBadKey) {
		e = fail(http.StatusNotFound, CodeNotFound, "this rotate link does not exist")
	}
	if errors.Is(err, auth.ErrTokenRevoked) || errors.Is(err, auth.ErrKeyRevoked) {
		e = fail(http.StatusNotFound, CodeNotFound, "this rotate link was revoked")
	}

	if !wantsHTML(r) {
		writeError(w, r, e)
		return
	}
	if e.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(secondsFor(e.RetryAfter)))
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(e.Status)
	linkPage.Execute(w, linkPageData{Error: e.Message})
}

type recorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newRecorder() *recorder {
	return &recorder{header: http.Header{}, status: http.StatusOK}
}

func (rec *recorder) Header() http.Header { return rec.header }

func (rec *recorder) Write(b []byte) (int, error) { return rec.body.Write(b) }

func (rec *recorder) WriteHeader(status int) { rec.status = status }

func (rec *recorder) rotateResult() *RotateResult {
	if rec.status != http.StatusOK {
		return nil
	}
	var out RotateResult
	if err := json.Unmarshal(rec.body.Bytes(), &out); err != nil || out.OperationID == "" {
		return nil
	}
	return &out
}

func (rec *recorder) errorMessage() string {
	var out ErrorBody
	if err := json.Unmarshal(rec.body.Bytes(), &out); err == nil && out.Message != "" {
		return out.Message
	}
	return "the rotation could not be started"
}
