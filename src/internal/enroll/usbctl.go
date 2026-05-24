package enroll

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/netcfg"
)

const (
	DefaultSysfsRoot = "/sys"
	DefaultDevRoot   = "/dev"

	usbDevicesRel = "bus/usb/devices"
	netClassRel   = "class/net"

	portEnable  = "0\n"
	portDisable = "1\n"
)

var (
	ErrNoUSBDevice = errors.New("enroll: interface is not backed by a usb device")
	ErrNoPortCtl   = errors.New("enroll: hub port exposes no per-port disable control")
	ErrBadBusDev   = errors.New("enroll: usb bus and device numbers must both be positive")
	ErrNoIDPath    = errors.New("enroll: udevadm reports no ID_PATH for the interface")
	ErrBadDevName  = errors.New("enroll: malformed usb device name")
)

type USBOptions struct {
	SysfsRoot string
	DevRoot   string
	Exec      netcfg.Exec
	Reset     func(path string) error
}

type USBController struct {
	sysfsRoot string
	devRoot   string
	exec      netcfg.Exec
	reset     func(path string) error
}

func NewUSBController(o USBOptions) *USBController {
	if o.SysfsRoot == "" {
		o.SysfsRoot = DefaultSysfsRoot
	}
	if o.DevRoot == "" {
		o.DevRoot = DefaultDevRoot
	}
	if o.Exec == nil {
		o.Exec = netcfg.SystemExec
	}
	if o.Reset == nil {
		o.Reset = ioctlReset
	}
	return &USBController{
		sysfsRoot: o.SysfsRoot,
		devRoot:   o.DevRoot,
		exec:      o.Exec,
		reset:     o.Reset,
	}
}

func (c *USBController) SysfsRoot() string { return c.sysfsRoot }

func (c *USBController) devicesDir() string { return filepath.Join(c.sysfsRoot, usbDevicesRel) }

func (c *USBController) netDir() string { return filepath.Join(c.sysfsRoot, netClassRel) }

func (c *USBController) DeviceName(iface string) (string, error) {
	link := filepath.Join(c.netDir(), iface, "device")
	target, err := filepath.EvalSymlinks(link)
	if err != nil {
		return "", fmt.Errorf("%w: %s: %v", ErrNoUSBDevice, iface, err)
	}
	name := StripInterface(filepath.Base(target))
	if !ValidDeviceName(name) {
		return "", fmt.Errorf("%w: %s resolves to %q", ErrNoUSBDevice, iface, filepath.Base(target))
	}
	if _, err := os.Stat(filepath.Join(c.devicesDir(), name)); err != nil {
		return "", fmt.Errorf("%w: %s has no entry under %s", ErrNoUSBDevice, name, c.devicesDir())
	}
	return name, nil
}

func StripInterface(name string) string {
	if i := strings.IndexByte(name, ':'); i >= 0 {
		return name[:i]
	}
	return name
}

func ValidDeviceName(name string) bool {
	bus, rest, ok := strings.Cut(name, "-")
	if !ok || bus == "" || rest == "" {
		return false
	}
	if _, err := strconv.Atoi(bus); err != nil {
		return false
	}
	for _, p := range strings.Split(rest, ".") {
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 {
			return false
		}
	}
	return true
}

func (c *USBController) BusDev(dev string) (int, int, error) {
	bus, err := c.readIntAttr(dev, "busnum")
	if err != nil {
		return 0, 0, err
	}
	num, err := c.readIntAttr(dev, "devnum")
	if err != nil {
		return 0, 0, err
	}
	if bus <= 0 || num <= 0 {
		return 0, 0, fmt.Errorf("%w: %s reports bus=%d dev=%d", ErrBadBusDev, dev, bus, num)
	}
	return bus, num, nil
}

func (c *USBController) readIntAttr(dev, attr string) (int, error) {
	raw, err := os.ReadFile(filepath.Join(c.devicesDir(), dev, attr))
	if err != nil {
		return 0, fmt.Errorf("%w: %s/%s: %v", ErrNoUSBDevice, dev, attr, err)
	}
	return strconv.Atoi(strings.TrimSpace(string(raw)))
}

func (c *USBController) DevNode(bus, dev int) string {
	return filepath.Join(c.devRoot, "bus", "usb", fmt.Sprintf("%03d", bus), fmt.Sprintf("%03d", dev))
}

func (c *USBController) ResetDevice(bus, dev int) error {
	if bus <= 0 || dev <= 0 {
		return fmt.Errorf("%w: bus=%d dev=%d", ErrBadBusDev, bus, dev)
	}
	return c.reset(c.DevNode(bus, dev))
}

func (c *USBController) ResetIface(iface string) error {
	dev, err := c.DeviceName(iface)
	if err != nil {
		return err
	}
	bus, num, err := c.BusDev(dev)
	if err != nil {
		return err
	}
	return c.ResetDevice(bus, num)
}

func PortControl(dev string) (string, string, error) {
	dev = StripInterface(dev)
	if !ValidDeviceName(dev) {
		return "", "", fmt.Errorf("%w: %q", ErrBadDevName, dev)
	}
	if parent, port, ok := lastDot(dev); ok {
		return parent + ":1.0", parent + "-port" + port, nil
	}
	bus, port, _ := strings.Cut(dev, "-")
	return bus + "-0:1.0", "usb" + bus + "-port" + port, nil
}

func lastDot(dev string) (string, string, bool) {
	i := strings.LastIndexByte(dev, '.')
	if i < 0 {
		return dev, "", false
	}
	return dev[:i], dev[i+1:], true
}

func (c *USBController) PortPath(dev string) (string, error) {
	hub, port, err := PortControl(dev)
	if err != nil {
		return "", err
	}
	p := filepath.Join(c.devicesDir(), hub, port, "disable")
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("%w: %s: %v", ErrNoPortCtl, p, err)
	}
	return p, nil
}

func (c *USBController) DisablePort(dev string) error { return c.writePort(dev, portDisable) }

func (c *USBController) EnablePort(dev string) error { return c.writePort(dev, portEnable) }

func (c *USBController) writePort(dev, value string) error {
	p, err := c.PortPath(dev)
	if err != nil {
		return err
	}
	if err := os.WriteFile(p, []byte(value), 0o644); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrNoPortCtl, p, err)
	}
	return nil
}

func (c *USBController) PortDisabled(dev string) (bool, error) {
	p, err := c.PortPath(dev)
	if err != nil {
		return false, err
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return false, fmt.Errorf("%w: %s: %v", ErrNoPortCtl, p, err)
	}
	return strings.TrimSpace(string(raw)) == "1", nil
}

func (c *USBController) IDPath(ctx context.Context, iface string) (string, error) {
	out, err := c.exec(ctx, "udevadm", "info", "--query=property",
		"--path="+filepath.Join(c.netDir(), iface))
	if err != nil {
		return "", fmt.Errorf("%w: %s: %v", ErrNoIDPath, iface, err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "ID_PATH="); ok && v != "" {
			return v, nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrNoIDPath, iface)
}

type USBNet struct {
	Iface       string
	Device      string
	Provisioned bool
	Slot        domain.Slot
}

func (c *USBController) USBNets() ([]USBNet, error) {
	entries, err := os.ReadDir(c.netDir())
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrNoUSBDevice, c.netDir(), err)
	}
	var out []USBNet
	for _, e := range entries {
		iface := e.Name()
		dev, err := c.DeviceName(iface)
		if err != nil {
			continue
		}
		slot, provisioned := domain.ParseIfaceName(iface)
		out = append(out, USBNet{Iface: iface, Device: dev, Provisioned: provisioned, Slot: slot})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Iface < out[j].Iface })
	return out, nil
}
