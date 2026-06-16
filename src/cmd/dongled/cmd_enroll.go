package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/n4darae/huawei-API/src/internal/config"
	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/device/hilink"
	"github.com/n4darae/huawei-API/src/internal/device/sim"
	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/enroll"
	"github.com/n4darae/huawei-API/src/internal/fw"
	"github.com/n4darae/huawei-API/src/internal/netcfg"
	netcfgfake "github.com/n4darae/huawei-API/src/internal/netcfg/fake"
	netcfglinux "github.com/n4darae/huawei-API/src/internal/netcfg/linux"
	"github.com/n4darae/huawei-API/src/internal/proxysup"
	"github.com/n4darae/huawei-API/src/internal/secrets"
	"github.com/n4darae/huawei-API/src/internal/store"
)

var errNotFarmHost = errors.New("this host is not marked as a farm host")

func init() {
	Register(Command{
		Name:  "enroll",
		Usage: "provision one dongle into a slot (dongled enroll -- --slot N)",
		Run:   runEnroll,
	})
}

func runEnroll(ctx context.Context, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet(config.Product+" enroll", flag.ContinueOnError)
	slot := fs.Int("slot", 0, "slot number 1-48, 0 allocates the lowest free slot")
	carrier := fs.String("carrier", "", "carrier name recorded on the dongle row")
	wait := fs.Duration("wait", enroll.DefaultLinkWait, "how long to wait for the dongle to enumerate")
	rediscover := fs.Duration("rediscover", enroll.DefaultRediscover, "how long to wait for the dongle at its new lan address")
	sysfs := fs.String("sysfs", enroll.DefaultSysfsRoot, "sysfs root, for testing against a fixture tree")
	noUSB := fs.Bool("no-usb-guard", false, "do not disable the usb ports of the other un-provisioned slots")
	skipPreflight := fs.Bool("skip-preflight", false, "enrol even though the fatal preflight checks are red")
	force := fs.Bool("force", false, "run even though "+config.FarmMarker+" is absent")
	asJSON := fs.Bool("json", false, "emit the result as json")
	if err := parseSubFlags(fs, args); err != nil {
		return err
	}
	if rest := fs.Args(); len(rest) == 1 && *slot == 0 {
		n, err := parsePositiveInt(rest[0])
		if err != nil {
			return fmt.Errorf("enroll: %q is not a slot number", rest[0])
		}
		*slot = n
	}
	if *slot != 0 && !domain.Slot(*slot).Valid() {
		return fmt.Errorf("enroll: slot %d is outside 1-%d", *slot, domain.MaxSlots)
	}
	if err := requireFarmHost(*force); err != nil {
		return err
	}
	if !cfg.PublicHost.IsValid() {
		return config.ErrPublicHostMissing
	}

	if !*skipPreflight {
		report := enroll.Preflight(ctx, preflightOptions(cfg))
		if !report.Green(true) {
			fmt.Fprint(os.Stderr, report.FatalFailed().Text())
			return errors.New("enroll: the host is not ready; fix the fatal checks or pass --skip-preflight")
		}
	}

	nc, err := buildNetcfg(cfg)
	if err != nil {
		return err
	}
	if err := nc.EnsureRouteTableNames(ctx); err != nil {
		return err
	}
	if err := nc.EnsureGlobal(ctx, []netip.Addr{cfg.PublicHost}); err != nil {
		return err
	}

	firewall, err := buildFirewall(ctx, cfg)
	if err != nil {
		return err
	}
	devices, closeDevices, err := buildDevices(cfg)
	if err != nil {
		return err
	}
	defer closeDevices()

	repos, closeStore, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeStore()

	supervisor, err := buildSupervisor(cfg)
	if err != nil {
		return err
	}

	e, err := enroll.New(enroll.Deps{
		NodeID:          cfg.NodeID,
		PublicHost:      cfg.PublicHost,
		NServerFallback: cfg.NServerFallback,
		Netcfg:          nc,
		Firewall:        firewall,
		Devices:         devices,
		Repos:           repos,
		Supervisor:      supervisor,
		USB:             enroll.NewUSBController(enroll.USBOptions{SysfsRoot: *sysfs}),
		SkipUSBGuard:    *noUSB,
		LinkWait:        *wait,
		Rediscover:      *rediscover,
		Selftest:        nil,
		Progress: func(ev enroll.Event) {
			if *asJSON {
				return
			}
			if ev.Error != "" {
				fmt.Fprintf(os.Stderr, "[%d/%d] %s: %s\n", ev.Index, ev.Total, ev.Step, ev.Error)
				return
			}
			fmt.Printf("[%d/%d] %s: %s\n", ev.Index, ev.Total, ev.Step, ev.Detail)
		},
	})
	if err != nil {
		return err
	}

	res, err := e.Enroll(ctx, enroll.Request{Slot: domain.Slot(*slot), Carrier: *carrier})
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(res)
	}
	printEnrollSummary(cfg, res)
	return nil
}

func printEnrollSummary(cfg config.Config, res *enroll.Result) {
	fmt.Println()
	fmt.Printf("slot        %s (%s, uid %d)\n", res.Slot, res.IfName, res.Slot.UID())
	fmt.Printf("device      %s imei %s\n", res.DeviceName, res.IMEI)
	fmt.Printf("iccid       %s\n", res.ICCID)
	fmt.Printf("firmware    %s\n", res.Firmware)
	fmt.Printf("id path     %s\n", res.IDPath)
	fmt.Printf("usb path    %s\n", res.USBPath)
	fmt.Printf("lan ip      %s (change supported: %t)\n", res.LanIP, res.LanIPChangeSupported)
	fmt.Printf("socks5      %s:%d\n", cfg.PublicHost, res.SocksPort)
	fmt.Printf("http        %s:%d\n", cfg.PublicHost, res.HTTPPort)
	fmt.Printf("credentials %s:%s\n", res.Username, res.Password)
	fmt.Printf("operation   %s\n", res.OperationID)
	if !res.SelftestRan {
		fmt.Printf("selftest    NOT RUN: %s\n", res.SelftestNote)
	}
	if !res.LanIPChangeSupported {
		fmt.Printf("\nThis dongle cannot move its LAN subnet. The slot needs the manual\n" +
			"namespace procedure in docs/OPERATIONS.md before it can carry traffic.\n")
	}
	fmt.Printf("\nLabel the physical port %q and plug in the next dongle.\n", res.USBPath)
}

func requireFarmHost(force bool) error {
	if force {
		return nil
	}
	if _, err := os.Stat(config.FarmMarker); err == nil {
		return nil
	}
	return fmt.Errorf("%w: %s is absent. This command rewrites ip rules, systemd-networkd files and nft sets in the root network namespace. Create the marker on the real farm host, or pass --force if you are certain", errNotFarmHost, config.FarmMarker)
}

func parsePositiveInt(s string) (int, error) {
	n := 0
	if s == "" {
		return 0, errors.New("empty")
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errors.New("not a number")
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

func buildNetcfg(cfg config.Config) (netcfg.Manager, error) {
	switch cfg.Netcfg {
	case config.BackendLinux:
		return netcfglinux.New(netcfglinux.Options{
			NetworkDir:    cfg.NetworkDir,
			Exec:          netcfg.SystemExec,
			RequireIDPath: true,
		}), nil
	case config.BackendFake:
		return netcfgfake.New(), nil
	default:
		return nil, fmt.Errorf("%w: netcfg %q", config.ErrBadBackend, string(cfg.Netcfg))
	}
}

func buildFirewall(ctx context.Context, cfg config.Config) (fw.Firewall, error) {
	switch cfg.Firewall {
	case config.BackendNft:
		n := fw.NewNft(fw.Options{})
		if err := n.Verify(ctx); err != nil {
			return nil, err
		}
		return n, nil
	case config.BackendFake:
		return fw.NewFake(), nil
	default:
		return nil, fmt.Errorf("%w: fw %q", config.ErrBadBackend, string(cfg.Firewall))
	}
}

func buildDevices(cfg config.Config) (device.Registry, func(), error) {
	switch cfg.Device {
	case config.BackendHiLink:
		r := hilink.NewRegistry(hilink.RegistryOptions{
			Options: hilink.Options{Timeout: hilink.DefaultTimeout},
		})
		return r, func() { r.Close() }, nil
	case config.BackendSim:
		farm := sim.NewFarm(cfg.SimSlots, sim.FarmOptions{FactoryDefaultLAN: true})
		return farm.Registry(), func() { farm.Close() }, nil
	default:
		return nil, nil, fmt.Errorf("%w: device %q", config.ErrBadBackend, string(cfg.Device))
	}
}

func buildSupervisor(cfg config.Config) (proxysup.Supervisor, error) {
	switch cfg.Proxy {
	case config.BackendSystemd:
		return proxysup.NewSystemd(proxysup.Options{
			Bin:        cfg.Bin3proxy,
			LogDir:     cfg.LogDir,
			InternalIP: cfg.PublicHost,
		})
	case config.BackendFake:
		return nil, fmt.Errorf("%w: the fake proxy backend cannot enrol a real slot", domain.ErrNotImplemented)
	default:
		return nil, fmt.Errorf("%w: proxy %q", config.ErrBadBackend, string(cfg.Proxy))
	}
}

func openStore(ctx context.Context, cfg config.Config) (store.Repos, func(), error) {
	sealer, err := loadSealer(cfg)
	if err != nil {
		return nil, nil, err
	}
	s, err := store.Open(cfg.DBPath, sealer)
	if err != nil {
		return nil, nil, err
	}
	if err := s.Migrate(ctx); err != nil {
		s.Close()
		return nil, nil, err
	}
	if err := ensureNode(ctx, s, cfg); err != nil {
		s.Close()
		return nil, nil, err
	}
	return s, func() { s.Close() }, nil
}

func loadSealer(cfg config.Config) (secrets.Sealer, error) {
	if dir, ok := os.LookupEnv("CREDENTIALS_DIRECTORY"); ok {
		if sealer, err := secrets.LoadKEK(dir + "/" + config.KEKCredName); err == nil {
			return sealer, nil
		}
	}
	return secrets.LoadKEK(kekPath(cfg))
}

func kekPath(cfg config.Config) string {
	return strings.TrimSuffix(cfg.EtcDir, "/") + "/kek.cred"
}

func ensureNode(ctx context.Context, s *store.Store, cfg config.Config) error {
	_, err := s.Nodes().Get(ctx, cfg.NodeID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	now := time.Now().UnixMilli()
	return s.Nodes().Upsert(ctx, domain.Node{
		ID:         cfg.NodeID,
		Name:       cfg.NodeName,
		Kind:       domain.NodeKindLocal,
		PublicHost: cfg.PublicHost,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
}
