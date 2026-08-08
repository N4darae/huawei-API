package config

import (
	"errors"
	"flag"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	EphemeralPortMin = 32768
	EphemeralPortMax = 60999
)

type Backend string

const (
	BackendLinux   Backend = "linux"
	BackendFake    Backend = "fake"
	BackendHiLink  Backend = "hilink"
	BackendSim     Backend = "sim"
	BackendSystemd Backend = "systemd"
	BackendNft     Backend = "nft"
)

type Carrier struct {
	Name              string
	HoldEscalate      []time.Duration
	WaitConnect       time.Duration
	VerifyTimeout     time.Duration
	HardDeadline      time.Duration
	PollInterval      time.Duration
	MaxAttempts       int
	MinRotateInterval time.Duration
}

type Reconcile struct {
	SweepInterval       time.Duration
	StartupGrace        time.Duration
	BackoffMin          time.Duration
	BackoffMax          time.Duration
	RebootBudgetPerDay  int
	RebootCooldown      time.Duration
	MaxConcurrentRotate int
	RotateJitter        time.Duration
}

type Config struct {
	NodeID     string
	NodeName   string
	PublicHost netip.Addr

	PanelAddr   string
	MetricsAddr string

	EtcDir     string
	RunDir     string
	StateDir   string
	LogDir     string
	BackupDir  string
	BinDir     string
	NetworkDir string
	DBPath     string
	Bin3proxy  string

	Netcfg   Backend
	Device   Backend
	Proxy    Backend
	Firewall Backend

	SimSlots int
	DevSeed  bool
	LogLevel string

	NServerFallback netip.Addr
	SessionTTL      time.Duration
	ShutdownGrace   time.Duration

	Carrier   Carrier
	Reconcile Reconcile
}

func Default() Config {
	return Config{
		NodeID:      "local",
		NodeName:    "local",
		PanelAddr:   PanelAddr,
		MetricsAddr: MetricsAddr,

		EtcDir:     EtcDir,
		RunDir:     RunDir,
		StateDir:   StateDir,
		LogDir:     LogDir,
		BackupDir:  BackupDir,
		BinDir:     BinDir,
		NetworkDir: NetworkDir,
		DBPath:     DBPath,
		Bin3proxy:  Bin3proxy,

		Netcfg:   BackendFake,
		Device:   BackendSim,
		Proxy:    BackendFake,
		Firewall: BackendFake,

		SimSlots: 4,
		DevSeed:  false,
		LogLevel: "info",

		NServerFallback: netip.AddrFrom4([4]byte{1, 1, 1, 1}),
		SessionTTL:      12 * time.Hour,
		ShutdownGrace:   15 * time.Second,

		Carrier: Carrier{
			Name:              "default",
			HoldEscalate:      []time.Duration{6 * time.Second, 15 * time.Second, 40 * time.Second},
			WaitConnect:       45 * time.Second,
			VerifyTimeout:     10 * time.Second,
			HardDeadline:      90 * time.Second,
			PollInterval:      time.Second,
			MaxAttempts:       3,
			MinRotateInterval: 60 * time.Second,
		},
		Reconcile: Reconcile{
			SweepInterval:       10 * time.Second,
			StartupGrace:        180 * time.Second,
			BackoffMin:          2 * time.Second,
			BackoffMax:          5 * time.Minute,
			RebootBudgetPerDay:  4,
			RebootCooldown:      30 * time.Minute,
			MaxConcurrentRotate: 4,
			RotateJitter:        3 * time.Second,
		},
	}
}

func (c *Config) BindFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.NodeID, "node-id", c.NodeID, "node identifier")
	fs.StringVar(&c.NodeName, "node-name", c.NodeName, "node display name")
	fs.Func("public-host", "public IPv4 address bound as 3proxy internal", func(v string) error {
		a, err := netip.ParseAddr(v)
		if err != nil {
			return err
		}
		c.PublicHost = a
		return nil
	})
	fs.StringVar(&c.PanelAddr, "panel-addr", c.PanelAddr, "panel listen address")
	fs.StringVar(&c.MetricsAddr, "metrics-addr", c.MetricsAddr, "metrics listen address")
	fs.StringVar(&c.DBPath, "db", c.DBPath, "sqlite database path")
	fs.StringVar(&c.EtcDir, "etc-dir", c.EtcDir, "configuration directory holding kek.cred")
	fs.StringVar(&c.Bin3proxy, "3proxy-bin", c.Bin3proxy, "pinned 3proxy binary path")
	fs.StringVar(&c.LogLevel, "log-level", c.LogLevel, "debug|info|warn|error")
	fs.IntVar(&c.SimSlots, "sim-slots", c.SimSlots, "simulated dongle count when device backend is sim")
	fs.BoolVar(&c.DevSeed, "dev-seed", c.DevSeed, "seed a development admin account")
	backendVar(fs, &c.Netcfg, "netcfg", "linux|fake")
	backendVar(fs, &c.Device, "device", "hilink|sim")
	backendVar(fs, &c.Proxy, "proxy", "systemd|fake")
	backendVar(fs, &c.Firewall, "fw", "nft|fake")
}

func backendVar(fs *flag.FlagSet, dst *Backend, name, usage string) {
	fs.Func(name, usage, func(v string) error {
		*dst = Backend(strings.ToLower(strings.TrimSpace(v)))
		return nil
	})
}

func (c *Config) ApplyEnv(lookup func(string) (string, bool)) error {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	if v, ok := lookup("DONGLED_PUBLIC_HOST"); ok {
		a, err := netip.ParseAddr(v)
		if err != nil {
			return fmt.Errorf("DONGLED_PUBLIC_HOST: %w", err)
		}
		c.PublicHost = a
	}
	if v, ok := lookup("DONGLED_NODE_ID"); ok {
		c.NodeID = v
	}
	if v, ok := lookup("DONGLED_PANEL_ADDR"); ok {
		c.PanelAddr = v
	}
	if v, ok := lookup("DONGLED_METRICS_ADDR"); ok {
		c.MetricsAddr = v
	}
	if v, ok := lookup("DONGLED_DB"); ok {
		c.DBPath = v
	}
	if v, ok := lookup("DONGLED_ETC_DIR"); ok {
		c.EtcDir = v
	}
	if v, ok := lookup("DONGLED_LOG_LEVEL"); ok {
		c.LogLevel = v
	}
	if v, ok := lookup("DONGLED_NETCFG"); ok {
		c.Netcfg = Backend(v)
	}
	if v, ok := lookup("DONGLED_DEVICE"); ok {
		c.Device = Backend(v)
	}
	if v, ok := lookup("DONGLED_PROXY"); ok {
		c.Proxy = Backend(v)
	}
	if v, ok := lookup("DONGLED_FW"); ok {
		c.Firewall = Backend(v)
	}
	if v, ok := lookup("DONGLED_SIM_SLOTS"); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("DONGLED_SIM_SLOTS: %w", err)
		}
		c.SimSlots = n
	}
	return nil
}

var (
	ErrPublicHostMissing = errors.New("config: public_host is required")
	ErrPublicHostNotPub  = errors.New("config: public_host must be a global unicast address")
	ErrBadBackend        = errors.New("config: unknown backend")
	ErrBadListenAddr     = errors.New("config: listen address must be host:port with port below the ephemeral range")
)

func (c Config) Validate() error {
	if !c.PublicHost.IsValid() {
		return ErrPublicHostMissing
	}
	if !IsPublicUnicast(c.PublicHost) {
		return fmt.Errorf("%w: %s", ErrPublicHostNotPub, c.PublicHost)
	}
	if err := validListen(c.PanelAddr); err != nil {
		return err
	}
	if err := validListen(c.MetricsAddr); err != nil {
		return err
	}
	if err := oneOf(c.Netcfg, BackendLinux, BackendFake); err != nil {
		return err
	}
	if err := oneOf(c.Device, BackendHiLink, BackendSim); err != nil {
		return err
	}
	if err := oneOf(c.Proxy, BackendSystemd, BackendFake); err != nil {
		return err
	}
	if err := oneOf(c.Firewall, BackendNft, BackendFake); err != nil {
		return err
	}
	if c.NodeID == "" {
		return errors.New("config: node_id is required")
	}
	if len(c.Carrier.HoldEscalate) == 0 {
		return errors.New("config: carrier hold escalation ladder is empty")
	}
	if c.Reconcile.MaxConcurrentRotate < 1 {
		return errors.New("config: max concurrent rotate must be >= 1")
	}
	return nil
}

func oneOf(got Backend, allowed ...Backend) error {
	for _, a := range allowed {
		if got == a {
			return nil
		}
	}
	return fmt.Errorf("%w: %q", ErrBadBackend, string(got))
}

func validListen(addr string) error {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return fmt.Errorf("%w: %q", ErrBadListenAddr, addr)
	}
	p, err := strconv.Atoi(addr[i+1:])
	if err != nil || p < 1 || p >= EphemeralPortMin {
		return fmt.Errorf("%w: %q", ErrBadListenAddr, addr)
	}
	return nil
}

func IsPublicUnicast(a netip.Addr) bool {
	if !a.IsValid() || !a.Is4() {
		return false
	}
	if a.IsUnspecified() || a.IsLoopback() || a.IsMulticast() || a.IsLinkLocalUnicast() || a.IsPrivate() {
		return false
	}
	b := a.As4()
	if b[0] == 100 && b[1] >= 64 && b[1] <= 127 {
		return false
	}
	if b[0] == 0 || b[0] >= 240 {
		return false
	}
	return true
}
