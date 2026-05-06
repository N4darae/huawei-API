package hilink

import (
	"bytes"
	"embed"
	"encoding/xml"
	"io/fs"
	"math"
	"net/netip"
	"strconv"
	"strings"
)

//go:embed testdata/*.xml
var fixtures embed.FS

func Fixtures() fs.FS {
	sub, err := fs.Sub(fixtures, "testdata")
	if err != nil {
		panic(err)
	}
	return sub
}

func Fixture(name string) []byte {
	b, err := fixtures.ReadFile("testdata/" + name)
	if err != nil {
		return nil
	}
	return b
}

const XMLProlog = `<?xml version="1.0" encoding="UTF-8"?>`

const ContentTypeXML = "text/xml; charset=UTF-8"

const ContentTypeResponse = "text/html"

func MarshalRequest(v any) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(XMLProlog)
	enc := xml.NewEncoder(&buf)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Flush(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func Unmarshal(body []byte, out any) error {
	if out == nil {
		return nil
	}
	return xml.Unmarshal(body, out)
}

func rootElement(body []byte) string {
	dec := xml.NewDecoder(bytes.NewReader(body))
	for {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		if se, ok := tok.(xml.StartElement); ok {
			return se.Name.Local
		}
	}
}

func atoi(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

func atoi64(s string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func itoa(n int) string { return strconv.Itoa(n) }

func bit(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func isSet(s string) bool { return strings.TrimSpace(s) == "1" }

func suffixInt(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	end := 0
	for end < len(s) {
		c := s[end]
		if c == '-' || c == '+' || c == '.' || (c >= '0' && c <= '9') {
			end++
			continue
		}
		break
	}
	num := s[:end]
	if num == "" || num == "-" || num == "+" {
		return 0
	}
	if strings.ContainsRune(num, '.') {
		f, err := strconv.ParseFloat(num, 64)
		if err != nil {
			return 0
		}
		return int(math.Round(f))
	}
	n, err := strconv.Atoi(num)
	if err != nil {
		return 0
	}
	return n
}

func parseAddr(s string) netip.Addr {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return netip.Addr{}
	}
	return a
}

func addrString(a netip.Addr) string {
	if !a.IsValid() {
		return ""
	}
	return a.String()
}

func parseAddrList(parts ...string) []netip.Addr {
	out := make([]netip.Addr, 0, len(parts))
	for _, p := range parts {
		if a := parseAddr(p); a.IsValid() {
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func netmaskAddr(a netip.Addr) netip.Addr {
	if a.IsValid() {
		return a
	}
	return netip.AddrFrom4([4]byte{255, 255, 255, 0})
}
