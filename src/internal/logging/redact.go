package logging

import (
	"context"
	"io"
	"log/slog"
	"regexp"
	"strings"
)

const Redacted = "[redacted]"

const uriPatternIndex = 3

var sensitiveKeys = []string{
	"api_key",
	"apikey",
	"authorization",
	"bearer",
	"cookie",
	"credential",
	"csrf",
	"kek",
	"link_token",
	"passwd",
	"password",
	"proxy_pass",
	"pwd",
	"secret",
	"session",
	"set-cookie",
	"token",
	"x-csrf-token",
}

var valuePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(dgl_live_)[A-Za-z0-9_\-]+`),
	regexp.MustCompile(`(?i)\b(bearer\s+)[A-Za-z0-9._\-+/=]+`),
	regexp.MustCompile(`(?i)((?:__Host-)?[A-Za-z0-9_\-]*(?:session|token|csrf|secret|password|passwd|pwd|api_?key|kek)[A-Za-z0-9_\-]*"?\s*[=:]\s*"?)[^\s",;&}]+`),
	regexp.MustCompile(`(?i)\b(socks5|socks4|https?)(://[^:/\s]+:)[^@\s]+@`),
}

func SensitiveKey(key string) bool {
	k := strings.ToLower(key)
	for _, s := range sensitiveKeys {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

func RedactString(s string) string {
	out := s
	for i, re := range valuePatterns {
		if i == uriPatternIndex {
			out = re.ReplaceAllString(out, "${1}${2}"+Redacted+"@")
			continue
		}
		out = re.ReplaceAllString(out, "${1}"+Redacted)
	}
	return out
}

func RedactValue(v slog.Value) slog.Value {
	switch v.Kind() {
	case slog.KindString:
		return slog.StringValue(RedactString(v.String()))
	case slog.KindGroup:
		return slog.GroupValue(RedactAttrs(v.Group())...)
	case slog.KindLogValuer:
		return RedactValue(v.Resolve())
	default:
		return v
	}
}

func RedactAttr(a slog.Attr) slog.Attr {
	if SensitiveKey(a.Key) {
		return slog.String(a.Key, Redacted)
	}
	a.Value = RedactValue(a.Value)
	return a
}

func RedactAttrs(in []slog.Attr) []slog.Attr {
	out := make([]slog.Attr, 0, len(in))
	for _, a := range in {
		out = append(out, RedactAttr(a))
	}
	return out
}

type Handler struct {
	inner slog.Handler
}

var _ slog.Handler = (*Handler)(nil)

func NewHandler(inner slog.Handler) *Handler {
	if inner == nil {
		inner = slog.NewTextHandler(io.Discard, nil)
	}
	if h, ok := inner.(*Handler); ok {
		return h
	}
	return &Handler{inner: inner}
}

func (h *Handler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	clean := slog.NewRecord(r.Time, r.Level, RedactString(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		clean.AddAttrs(RedactAttr(a))
		return true
	})
	return h.inner.Handle(ctx, clean)
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &Handler{inner: h.inner.WithAttrs(RedactAttrs(attrs))}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{inner: h.inner.WithGroup(name)}
}

func New(w io.Writer, level string) *slog.Logger {
	return slog.New(NewHandler(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: ParseLevel(level)})))
}

func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
