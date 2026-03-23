package sim

import (
	"bytes"
	"encoding/xml"
	"net/http"
	"strings"

	"github.com/n4darae/huawei-API/src/internal/device/hilink"
	"github.com/n4darae/huawei-API/src/internal/domain"
)

func writeXML(w http.ResponseWriter, body []byte, cookie string, tokens []string) {
	h := w.Header()
	h.Set("Content-Type", hilink.ContentTypeResponse)
	if cookie != "" {
		h["Set-Cookie"] = []string{cookie + "; path=/"}
	}
	if len(tokens) > 0 {
		h[hilink.HeaderToken] = []string{strings.Join(tokens, hilink.TokenSeparator)}
	}
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

func writeAPIError(w http.ResponseWriter, cookie string, code domain.HiLinkCode) {
	writeXML(w, errorBody(code), cookie, nil)
}

func errorBody(code domain.HiLinkCode) []byte {
	if b := hilink.Fixture("error_" + itoa(int(code)) + ".xml"); b != nil {
		return b
	}
	return hilink.EncodeError(code)
}

func escapeText(s string) string {
	var buf bytes.Buffer
	if err := xml.EscapeText(&buf, []byte(s)); err != nil {
		return ""
	}
	return buf.String()
}

func rewriteElements(src []byte, vals map[string]string) []byte {
	if len(vals) == 0 {
		return src
	}
	out := make([]byte, 0, len(src)+64)
	i := 0
	for i < len(src) {
		lt := bytes.IndexByte(src[i:], '<')
		if lt < 0 {
			out = append(out, src[i:]...)
			break
		}
		lt += i
		out = append(out, src[i:lt]...)
		gt := bytes.IndexByte(src[lt:], '>')
		if gt < 0 {
			out = append(out, src[lt:]...)
			break
		}
		gt += lt
		tag := string(src[lt+1 : gt])
		out = append(out, src[lt:gt+1]...)
		i = gt + 1
		if tag == "" || tag[0] == '/' || tag[0] == '?' || tag[0] == '!' || strings.HasSuffix(tag, "/") {
			continue
		}
		name := tag
		if sp := strings.IndexAny(name, " \t\r\n"); sp >= 0 {
			name = name[:sp]
		}
		v, ok := vals[name]
		if !ok {
			continue
		}
		closing := []byte("</" + name + ">")
		end := bytes.Index(src[i:], closing)
		if end < 0 {
			continue
		}
		out = append(out, escapeText(v)...)
		i += end
	}
	return out
}

func innerBlock(src []byte, tag string) []byte {
	open := []byte("<" + tag + ">")
	closing := []byte("</" + tag + ">")
	s := bytes.Index(src, open)
	if s < 0 {
		return nil
	}
	s += len(open)
	e := bytes.Index(src[s:], closing)
	if e < 0 {
		return nil
	}
	return src[s : s+e]
}

func replaceBlock(src []byte, tag string, body []byte) []byte {
	open := []byte("<" + tag + ">")
	closing := []byte("</" + tag + ">")
	s := bytes.Index(src, open)
	if s < 0 {
		return src
	}
	inner := s + len(open)
	e := bytes.Index(src[inner:], closing)
	if e < 0 {
		return src
	}
	out := make([]byte, 0, len(src)+len(body))
	out = append(out, src[:inner]...)
	out = append(out, body...)
	out = append(out, src[inner+e:]...)
	return out
}

func dropElements(src []byte, names ...string) []byte {
	out := src
	for _, n := range names {
		open := []byte("<" + n + ">")
		closing := []byte("</" + n + ">")
		s := bytes.Index(out, open)
		if s < 0 {
			continue
		}
		e := bytes.Index(out[s:], closing)
		if e < 0 {
			continue
		}
		end := s + e + len(closing)
		for end < len(out) && (out[end] == '\n' || out[end] == '\r') {
			end++
		}
		next := make([]byte, 0, len(out))
		next = append(next, out[:s]...)
		next = append(next, out[end:]...)
		out = next
	}
	return out
}
