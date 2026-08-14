package hilink

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

const (
	DefaultTimeout   = 5 * time.Second
	ReachableTimeout = 2 * time.Second

	maxBodyBytes = 1 << 20

	apiPrefix = "/api/"
)

type Options struct {
	HTTPClient    *http.Client
	BaseURL       string
	Timeout       time.Duration
	OnTokenRefill func(n int)
	Sleep         func(context.Context, time.Duration)
}

type Client struct {
	base          netip.Addr
	baseURL       string
	hc            *http.Client
	timeout       time.Duration
	onTokenRefill func(n int)
	sleep         func(context.Context, time.Duration)
	sess          *session
}

func New(base netip.Addr, opt Options) *Client {
	c := &Client{
		base:          base,
		baseURL:       strings.TrimSuffix(opt.BaseURL, "/"),
		hc:            opt.HTTPClient,
		timeout:       opt.Timeout,
		onTokenRefill: opt.OnTokenRefill,
		sleep:         opt.Sleep,
		sess:          &session{},
	}
	if c.baseURL == "" && base.IsValid() {
		c.baseURL = "http://" + base.String()
	}
	if c.timeout <= 0 {
		c.timeout = DefaultTimeout
	}
	if c.hc == nil {
		c.hc = &http.Client{Timeout: c.timeout}
	}
	if c.sleep == nil {
		c.sleep = sleepCtx
	}
	return c
}

func (c *Client) Addr() netip.Addr { return c.base }

func (c *Client) BaseURL() string { return c.baseURL }

func (c *Client) Get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

func (c *Client) Post(ctx context.Context, path string, in any, out any) error {
	body, err := MarshalRequest(in)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, path, body, out)
}

func (c *Client) Reachable(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, ReachableTimeout)
	defer cancel()
	var out infoResponse
	err := c.do(ctx, http.MethodGet, "device/information", nil, &out)
	if err == nil {
		return true
	}
	return !errors.Is(err, domain.ErrUnreachable) && ctx.Err() == nil
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, out any) error {
	var last error
	for attempt := 0; attempt < MaxAttempts; attempt++ {
		if attempt > 0 {
			if err := c.sleepBackoff(ctx, attempt-1); err != nil {
				return err
			}
			c.sess.reset()
		}
		if err := c.ensureSession(ctx); err != nil {
			last = err
			if IsTokenError(err) {
				continue
			}
			return err
		}
		err := c.attempt(ctx, method, path, body, out)
		if err == nil {
			return nil
		}
		last = err
		if !IsTokenError(err) {
			return err
		}
	}
	return last
}

func (c *Client) sleepBackoff(ctx context.Context, i int) error {
	if i >= len(Backoffs) {
		i = len(Backoffs) - 1
	}
	c.sleep(ctx, Backoffs[i])
	return ctx.Err()
}

func (c *Client) ensureSession(ctx context.Context) error {
	if _, _, ok := c.sess.snapshot(); ok {
		return nil
	}
	return c.handshake(ctx)
}

func (c *Client) attempt(ctx context.Context, method, path string, body []byte, out any) error {
	var cookie, token string
	var ok bool
	if method == http.MethodPost {
		cookie, token, ok = c.sess.take()
	} else {
		cookie, token, ok = c.sess.snapshot()
	}
	if !ok {
		return domain.Wrap(domain.ErrTokenInvalid, "hilink: no session for %s", path)
	}
	req, err := c.newRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	req.Header.Set(HeaderCookie, cookie)
	req.Header.Set(HeaderToken, token)
	if method == http.MethodPost {
		req.Header.Set("Content-Type", ContentTypeXML)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return c.unreachable(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return c.unreachable(err)
	}
	c.sess.absorb(tokensFromHeader(resp.Header))
	if ck := cookieFromHeader(resp.Header); ck != "" {
		c.sess.install(ck, nil)
	}

	if apiErr := DecodeError(raw, path); apiErr != nil {
		return apiErr
	}
	if resp.StatusCode != http.StatusOK {
		return domain.Wrap(domain.ErrUnreachable, "hilink: %s %s status %d", method, path, resp.StatusCode)
	}
	return Unmarshal(raw, out)
}

func (c *Client) newRequest(ctx context.Context, method, path string, body []byte) (*http.Request, error) {
	url := c.baseURL + apiPrefix + strings.TrimPrefix(path, "/")
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "*/*")
	return req, nil
}

func (c *Client) unreachable(err error) error {
	return domain.Wrap(domain.ErrUnreachable, "hilink: %s: %v", c.baseURL, err)
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
