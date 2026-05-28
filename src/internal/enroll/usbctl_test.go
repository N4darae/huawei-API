package enroll

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/n4darae/huawei-API/src/internal/netcfg"
)

const fixtureSysfs = "testdata/sysfs"

func requireSysfsFixture(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("requires a real linux sysfs fixture, this host is " + runtime.GOOS)
	}
	if _, err := os.Stat(filepath.Join(fixtureSysfs, "class", "net", "dg01", "device")); err != nil {
		t.Skip("sysfs fixture testdata/sysfs is not checked out: " + err.Error())
	}
}

func copyTree(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case info.IsDir():
			return os.MkdirAll(target, 0o755)
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		default:
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			return os.WriteFile(target, data, 0o644)
		}
	})
	if err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	return dst
}

func TestPortControlDerivesTheHubPortFromTheDeviceName(t *testing.T) {
	cases := []struct {
		dev  string
		hub  string
		port string
	}{
		{"1-13.1", "1-13:1.0", "1-13-port1"},
		{"1-13.2", "1-13:1.0", "1-13-port2"},
		{"1-13.1:1.0", "1-13:1.0", "1-13-port1"},
		{"1-13", "1-0:1.0", "usb1-port13"},
		{"2-4", "2-0:1.0", "usb2-port4"},
		{"1-13.1.2", "1-13.1:1.0", "1-13.1-port2"},
	}
	for _, c := range cases {
		hub, port, err := PortControl(c.dev)
		if err != nil {
			t.Fatalf("PortControl(%q): %v", c.dev, err)
		}
		if hub != c.hub || port != c.port {
			t.Fatalf("PortControl(%q) = %q/%q, want %q/%q", c.dev, hub, port, c.hub, c.port)
		}
	}
	for _, bad := range []string{"", "12d1:14dc", "usb1", "1-", "-13", "1-x"} {
		if _, _, err := PortControl(bad); !errors.Is(err, ErrBadDevName) {
			t.Fatalf("PortControl(%q) accepted a name that is not a bus-port path: %v", bad, err)
		}
	}
}

func TestDeviceNameResolvesThroughTheInterfaceSymlink(t *testing.T) {
	requireSysfsFixture(t)
	c := NewUSBController(USBOptions{SysfsRoot: fixtureSysfs})

	got, err := c.DeviceName("dg01")
	if err != nil {
		t.Fatalf("DeviceName(dg01): %v", err)
	}
	if got != "1-13.1" {
		t.Fatalf("DeviceName(dg01) = %q, want 1-13.1", got)
	}
	if _, err := c.DeviceName("enp1s0f0"); !errors.Is(err, ErrNoUSBDevice) {
		t.Fatalf("a pci netdev must not resolve to a usb device, got %v", err)
	}
	if _, err := c.DeviceName("lo"); !errors.Is(err, ErrNoUSBDevice) {
		t.Fatalf("lo must not resolve to a usb device, got %v", err)
	}
}

func TestBusDevFeedsUsbresetWithNumbersNotVendorProduct(t *testing.T) {
	requireSysfsFixture(t)
	var reset []string
	c := NewUSBController(USBOptions{
		SysfsRoot: fixtureSysfs,
		DevRoot:   "/dev",
		Reset:     func(p string) error { reset = append(reset, p); return nil },
	})

	bus, dev, err := c.BusDev("1-13.1")
	if err != nil {
		t.Fatalf("BusDev: %v", err)
	}
	if bus != 1 || dev != 13 {
		t.Fatalf("BusDev = %d/%d, want 1/13", bus, dev)
	}
	if err := c.ResetIface("dg01"); err != nil {
		t.Fatalf("ResetIface: %v", err)
	}
	if len(reset) != 1 || reset[0] != "/dev/bus/usb/001/013" {
		t.Fatalf("reset targeted %v, want the single node /dev/bus/usb/001/013", reset)
	}
	for _, p := range reset {
		if strings.Contains(p, "12d1") || strings.Contains(p, "14dc") {
			t.Fatalf("reset addressed the device by vendor:product (%q), which resets the whole farm", p)
		}
	}
	if err := c.ResetDevice(0, 13); !errors.Is(err, ErrBadBusDev) {
		t.Fatalf("ResetDevice(0,13) must refuse, got %v", err)
	}
}

func TestDisableAndEnablePortToggleTheSysfsSwitch(t *testing.T) {
	requireSysfsFixture(t)
	root := copyTree(t, fixtureSysfs)
	c := NewUSBController(USBOptions{SysfsRoot: root})

	path, err := c.PortPath("1-13.1")
	if err != nil {
		t.Fatalf("PortPath: %v", err)
	}
	if err := c.DisablePort("1-13.1"); err != nil {
		t.Fatalf("DisablePort: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if strings.TrimSpace(string(raw)) != "1" {
		t.Fatalf("disable wrote %q", raw)
	}
	off, err := c.PortDisabled("1-13.1")
	if err != nil || !off {
		t.Fatalf("PortDisabled = %v, %v", off, err)
	}
	if err := c.EnablePort("1-13.1"); err != nil {
		t.Fatalf("EnablePort: %v", err)
	}
	raw, _ = os.ReadFile(path)
	if strings.TrimSpace(string(raw)) != "0" {
		t.Fatalf("enable wrote %q", raw)
	}
	if _, err := c.PortPath("3-9"); !errors.Is(err, ErrNoPortCtl) {
		t.Fatalf("a hub without per-port switching must be reported, got %v", err)
	}
}

func TestUSBNetsSeparatesProvisionedFromFactoryFresh(t *testing.T) {
	requireSysfsFixture(t)
	c := NewUSBController(USBOptions{SysfsRoot: fixtureSysfs})
	nets, err := c.USBNets()
	if err != nil {
		t.Fatalf("USBNets: %v", err)
	}
	if len(nets) != 3 {
		t.Fatalf("USBNets returned %d entries, want dg01, usb0 and usb1: %+v", len(nets), nets)
	}
	byName := map[string]USBNet{}
	for _, n := range nets {
		byName[n.Iface] = n
	}
	if !byName["dg01"].Provisioned || byName["dg01"].Slot != 1 {
		t.Fatalf("dg01 must be recognised as slot 1: %+v", byName["dg01"])
	}
	if byName["usb0"].Provisioned {
		t.Fatalf("usb0 has no slot yet and must count as un-provisioned")
	}
	if byName["usb0"].Device != "1-13.2" {
		t.Fatalf("usb0 resolved to %q, want 1-13.2", byName["usb0"].Device)
	}
}

func TestIDPathComesFromUdevadmAndNeverFromATemplate(t *testing.T) {
	const want = "pci-0000:00:14.0-usb-0:13.1:1.0"
	c := NewUSBController(USBOptions{
		SysfsRoot: fixtureSysfs,
		Exec: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			if name != "udevadm" {
				t.Fatalf("ID_PATH was read with %q, not udevadm", name)
			}
			return []byte("DEVPATH=/devices/pci0000:00/0000:00:14.0/usb1/1-13/1-13.1\n" +
				"ID_BUS=usb\nID_PATH=" + want + "\nID_PATH_TAG=pci_0000_00_14_0_usb_0_13_1_1_0\n"), nil
		},
	})
	got, err := c.IDPath(context.Background(), "dg01")
	if err != nil {
		t.Fatalf("IDPath: %v", err)
	}
	if got != want {
		t.Fatalf("IDPath = %q, want %q", got, want)
	}
	if strings.HasPrefix(got, "platform-") {
		t.Fatalf("xHCI is a PCI device; a platform- path means the .link will never match")
	}

	silent := NewUSBController(USBOptions{
		SysfsRoot: fixtureSysfs,
		Exec: func(context.Context, string, ...string) ([]byte, error) {
			return []byte("DEVPATH=/devices/virtual/net/dummy0\nID_BUS=\n"), nil
		},
	})
	if _, err := silent.IDPath(context.Background(), "dg01"); !errors.Is(err, ErrNoIDPath) {
		t.Fatalf("a missing ID_PATH must be a hard error, got %v", err)
	}
}

var _ netcfg.Exec = netcfg.SystemExec
