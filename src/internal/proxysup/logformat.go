package proxysup

import (
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

const (
	LogFieldCount  = 12
	LogTimeLayout  = "02-01-2006 15:04:05 -0700"
	logErrorNoAuth = "00005"
	logErrorBind   = "00012"
)

var ErrLogParse = errors.New("logformat: line does not match the frozen format")

type LogRecord struct {
	At          time.Time
	Service     string
	ServicePort int
	ErrorCode   string
	User        string
	Client      netip.AddrPort
	Remote      netip.AddrPort
	BytesOut    int64
	BytesIn     int64
	Hops        int
	Text        string
	External    netip.Addr
}

func (r LogRecord) AuthFailed() bool { return r.ErrorCode == logErrorNoAuth }

func (r LogRecord) BindFailed() bool { return r.ErrorCode == logErrorBind }

func (r LogRecord) OK() bool {
	return r.ErrorCode == "" || r.ErrorCode == "0" || r.ErrorCode == "00000"
}

func ParseLogLine(line string) (LogRecord, error) {
	f := strings.Fields(strings.TrimSpace(line))
	if len(f) < LogFieldCount {
		return LogRecord{}, fmt.Errorf("%w: %d fields", ErrLogParse, len(f))
	}

	var r LogRecord
	at, err := time.Parse(LogTimeLayout, f[0]+" "+f[1]+" "+f[2])
	if err != nil {
		return LogRecord{}, fmt.Errorf("%w: timestamp %q: %v", ErrLogParse, strings.Join(f[:3], " "), err)
	}
	r.At = at

	name, port, ok := strings.Cut(f[3], ".")
	if !ok {
		return LogRecord{}, fmt.Errorf("%w: service field %q", ErrLogParse, f[3])
	}
	r.Service = name
	if r.ServicePort, err = strconv.Atoi(port); err != nil {
		return LogRecord{}, fmt.Errorf("%w: service port %q", ErrLogParse, port)
	}

	r.ErrorCode = f[4]
	r.User = f[5]
	r.Client = parseAddrPort(f[6])
	r.Remote = parseAddrPort(f[7])

	if r.BytesOut, err = parseCount(f[8]); err != nil {
		return LogRecord{}, fmt.Errorf("%w: bytes out %q", ErrLogParse, f[8])
	}
	if r.BytesIn, err = parseCount(f[9]); err != nil {
		return LogRecord{}, fmt.Errorf("%w: bytes in %q", ErrLogParse, f[9])
	}
	if r.Hops, err = strconv.Atoi(f[10]); err != nil {
		r.Hops = 0
	}

	r.External, _ = netip.ParseAddr(f[len(f)-1])
	if len(f) > LogFieldCount {
		r.Text = strings.Join(f[11:len(f)-1], " ")
	}
	return r, nil
}

func parseCount(s string) (int64, error) {
	if s == "-" || s == "" {
		return 0, nil
	}
	return strconv.ParseInt(s, 10, 64)
}

func parseAddrPort(s string) netip.AddrPort {
	host, port, ok := strings.Cut(s, ":")
	if !ok {
		return netip.AddrPort{}
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.AddrPort{}
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 0 || p > 65535 {
		return netip.AddrPortFrom(a, 0)
	}
	return netip.AddrPortFrom(a, uint16(p))
}
