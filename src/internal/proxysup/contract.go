package proxysup

import (
	"context"
	"errors"
	"net/netip"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

const (
	LogFormat     = `"L%d-%m-%Y %H:%M:%S %z %N.%p %E %U %C:%c %R:%r %O %I %h %T %e"`
	Timeouts      = "1 5 30 60 180 1800 15 60 10 5"
	TimeoutsCount = 10
	AnonFlag      = "-a"
	FamilyFlag    = "-4"
	PasswordType  = "CL"
	PasswordLen   = 16
	NsCacheSize   = 65536
	LogRotateDays = 7
	LogDumpBytes  = 1048576
)

func ValidOperations() []string {
	return []string{
		"CONNECT", "BIND", "UDPASSOC", "ICMPASSOC",
		"HTTP_GET", "HTTP_PUT", "HTTP_POST", "HTTP_HEAD",
		"HTTP_CONNECT", "HTTP_OTHER", "HTTP", "HTTPS",
		"FTP_GET", "FTP_PUT", "FTP_LIST", "FTP_DATA", "FTP",
	}
}

type User struct {
	Name     string
	Password string
}

type Spec struct {
	Slot       domain.Slot
	InternalIP netip.Addr
	ExternalIP netip.Addr
	SocksPort  int
	HTTPPort   int
	Users      []User
	AuthMode   domain.AuthMode
	AuthIPs    []netip.Prefix
	Policy     domain.ProxyPolicy
	NServers   []netip.Addr
	LogPath    string
	ConfigPath string
	UID        int
	GID        int
}

type Status struct {
	Running     bool
	SocksBound  bool
	HTTPBound   bool
	ProbeOK     bool
	Unit        string
	ActiveState string
	SubState    string
	Since       int64
	Error       string
}

func (s Status) Healthy() bool { return s.SocksBound && s.HTTPBound && s.ProbeOK }

type Applied struct {
	Slot         domain.Slot
	ConfigPath   string
	ConfigSHA256 string
	Changed      bool
	Reloaded     bool
	Restarted    bool
	Status       Status
	AppliedAt    int64
}

type Supervisor interface {
	Apply(ctx context.Context, sp Spec) (Applied, error)
	Stop(ctx context.Context, slot domain.Slot, evict bool) error
	Status(ctx context.Context, slot domain.Slot) (Status, error)
}

var (
	ErrNoUsers      = errors.New("render: users list empty")
	ErrNoAuthStrong = errors.New("render: auth strong missing")
	ErrNoNoforce    = errors.New("render: noforce missing")
	ErrNoInternal   = errors.New("render: internal missing")
	ErrNoDenyAll    = errors.New("render: trailing deny * missing")
	ErrNoFlush      = errors.New("render: flush before ACL block missing")
	ErrBadAnonFlag  = errors.New("render: proxy line must use -a, never -a1/-a2")
	ErrUnquotedUser = errors.New("render: users line must be fully quoted")
	ErrNoAuthIPs    = errors.New("render: auth mode requires an ip whitelist but none is set")
	ErrUsersUnused  = errors.New("render: auth mode iplist must not emit a users line")
	ErrBadOperation = errors.New("render: unknown 3proxy operation keyword")
	ErrUIDMismatch  = errors.New("render: setuid/setgid must be derived from the same slot as the unit User/Group")
)

var (
	ErrValidateFailed = errors.New("validate: candidate config did not bind a listener")
	ErrNotBound       = errors.New("supervise: expected listener is not bound")
	ErrProbeFailed    = errors.New("supervise: synthetic authenticated probe failed")
)
