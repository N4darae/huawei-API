package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestRedactStringHidesEverySensitiveShape(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		absent []string
	}{
		{
			name:   "api key",
			in:     "customer authenticated with dgl_live_5Kq7mZr2xTn9wLb4VtY8sD1fG3hJ0nP",
			absent: []string{"5Kq7mZr2xTn9wLb4VtY8sD1fG3hJ0nP"},
		},
		{
			name:   "bearer header",
			in:     "Authorization: Bearer dgl_live_abcdefghijklmnop",
			absent: []string{"abcdefghijklmnop"},
		},
		{
			name:   "session cookie",
			in:     "Cookie: __Host-dongled_session=Zm9vYmFyYmF6cXV4; Path=/",
			absent: []string{"Zm9vYmFyYmF6cXV4"},
		},
		{
			name:   "csrf header value",
			in:     "x-csrf-token: 9f3c1d2e8a7b4c5d6e0f",
			absent: []string{"9f3c1d2e8a7b4c5d6e0f"},
		},
		{
			name:   "json password",
			in:     `{"username":"admin","password":"correct-horse-battery"}`,
			absent: []string{"correct-horse-battery"},
		},
		{
			name:   "proxy uri",
			in:     "socks5://cust_px01:Kq7mZr2xTn9wLb4V@203.0.113.10:21001",
			absent: []string{"Kq7mZr2xTn9wLb4V"},
		},
		{
			name:   "link token secret",
			in:     "issued link_token=1f2e3d4c5b6a7988 for key-1",
			absent: []string{"1f2e3d4c5b6a7988"},
		},
		{
			name:   "kek material",
			in:     "kek=8f14e45fceea167a5a36dedd4bea2543",
			absent: []string{"8f14e45fceea167a5a36dedd4bea2543"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RedactString(c.in)
			for _, secret := range c.absent {
				if strings.Contains(got, secret) {
					t.Fatalf("secret %q survived redaction: %s", secret, got)
				}
			}
			if !strings.Contains(got, Redacted) {
				t.Fatalf("nothing was redacted in %q, got %q", c.in, got)
			}
		})
	}
}

func TestRedactStringLeavesHarmlessTextAlone(t *testing.T) {
	in := "rotate proxy px01 finished in 41s with result changed"
	if got := RedactString(in); got != in {
		t.Fatalf("harmless message was rewritten: %q", got)
	}
}

func TestHandlerRedactsAttributesByKey(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(NewHandler(slog.NewTextHandler(&buf, nil)))

	log.Info("login",
		slog.String("username", "admin"),
		slog.String("password", "correct-horse"),
		slog.String("csrf_token", "9f3c1d2e"),
		slog.String("Authorization", "Bearer dgl_live_zzz"),
		slog.Group("key", slog.String("secret", "dgl_live_topsecret"), slog.String("id", "key-1")),
	)

	out := buf.String()
	for _, secret := range []string{"correct-horse", "9f3c1d2e", "dgl_live_zzz", "topsecret"} {
		if strings.Contains(out, secret) {
			t.Fatalf("secret %q leaked into the log line: %s", secret, out)
		}
	}
	for _, keep := range []string{"admin", "key-1"} {
		if !strings.Contains(out, keep) {
			t.Fatalf("non sensitive field %q was dropped: %s", keep, out)
		}
	}
}

func TestHandlerRedactsAttributesAttachedWithWith(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(NewHandler(slog.NewTextHandler(&buf, nil))).With(slog.String("api_key", "dgl_live_abc123"))

	log.Info("customer rotate accepted")

	if strings.Contains(buf.String(), "abc123") {
		t.Fatalf("WithAttrs bypassed redaction: %s", buf.String())
	}
}

func TestHandlerRedactsTheMessageItself(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(NewHandler(slog.NewTextHandler(&buf, nil)))

	log.Info("rejected Bearer dgl_live_leakedsecretvalue")

	if strings.Contains(buf.String(), "leakedsecretvalue") {
		t.Fatalf("message was not redacted: %s", buf.String())
	}
}

func TestHandlerKeepsLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	h := NewHandler(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	if h.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("info must stay disabled when the inner handler filters it")
	}
	if !h.Enabled(context.Background(), slog.LevelError) {
		t.Fatal("error must stay enabled")
	}
}

func TestNewHandlerDoesNotDoubleWrap(t *testing.T) {
	inner := NewHandler(slog.NewTextHandler(&bytes.Buffer{}, nil))
	if NewHandler(inner) != inner {
		t.Fatal("wrapping a redacting handler twice must be a no-op")
	}
}

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"INFO":    slog.LevelInfo,
		"warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
		"":        slog.LevelInfo,
	}
	for in, want := range cases {
		if got := ParseLevel(in); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}
