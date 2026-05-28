package proxysup

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/n4darae/huawei-API/src/internal/config"
)

type ValidateMode string

const (
	ValidateNetns   ValidateMode = "netns"
	ValidateScratch ValidateMode = "scratch"

	DefaultValidateTimeout = 3 * time.Second
	validatePollInterval   = 20 * time.Millisecond

	ScratchCaveat = "scratch validation rewrote internal to 127.0.0.1 and both service ports to " +
		"the scratch port, so it did not validate the exact bytes that will be installed"
)

var ErrNoNetns = errors.New("validate: cannot create a throwaway network namespace")

type ValidateRequest struct {
	Bin           string
	Config        []byte
	Spec          Spec
	ScratchPort   int
	Timeout       time.Duration
	WorkDir       string
	AllowFallback bool
	ForceMode     ValidateMode
}

type ValidateReport struct {
	Mode       ValidateMode
	Degraded   bool
	BoundPorts []int
	Stderr     string
	Note       string
}

func Validate(ctx context.Context, req ValidateRequest) (ValidateReport, error) {
	if req.Bin == "" {
		return ValidateReport{}, fmt.Errorf("%w: no 3proxy binary", ErrValidateFailed)
	}
	if len(req.Config) == 0 {
		return ValidateReport{}, fmt.Errorf("%w: empty candidate config", ErrValidateFailed)
	}
	if req.Timeout <= 0 {
		req.Timeout = DefaultValidateTimeout
	}
	if req.ScratchPort == 0 {
		req.ScratchPort = config.ProxyValidatePort
	}
	if req.WorkDir == "" {
		req.WorkDir = os.TempDir()
	}

	mode := req.ForceMode
	if mode == "" {
		mode = ValidateNetns
		if err := netnsUsable(); err != nil {
			if !req.AllowFallback {
				return ValidateReport{}, fmt.Errorf("%w: %v", ErrNoNetns, err)
			}
			mode = ValidateScratch
		}
	}

	if mode == ValidateNetns {
		rep, err := validateNetns(ctx, req)
		if err != nil && errors.Is(err, ErrNoNetns) && req.AllowFallback {
			return validateScratch(ctx, req)
		}
		return rep, err
	}
	return validateScratch(ctx, req)
}

func netnsUsable() error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("unsupported platform %s", runtime.GOOS)
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("euid %d cannot CLONE_NEWNET", os.Geteuid())
	}
	if _, err := exec.LookPath("ip"); err != nil {
		return fmt.Errorf("iproute2 is not installed: %w", err)
	}
	if _, err := exec.LookPath("sh"); err != nil {
		return fmt.Errorf("no shell: %w", err)
	}
	return nil
}

const netnsScript = `set -e
ip link set lo up
ip addr add "$1/32" dev lo 2>/dev/null || true
ip addr add "$2/32" dev lo 2>/dev/null || true
shift 2
exec "$@"`

func validateNetns(ctx context.Context, req ValidateRequest) (ValidateReport, error) {
	path, cleanup, err := writeCandidate(req.WorkDir, req.Config)
	if err != nil {
		return ValidateReport{}, err
	}
	defer cleanup()

	args := []string{
		"-c", netnsScript, "dongled-validate",
		req.Spec.InternalIP.String(), req.Spec.ExternalIP.String(),
		req.Bin, path,
	}
	cmd := exec.Command("sh", args...)
	cmd.SysProcAttr = netnsSysProcAttr()

	want := req.Spec.Ports()
	rep, err := runAndWait(ctx, cmd, want, len(want), req.Timeout)
	rep.Mode = ValidateNetns
	if err != nil && isPermissionError(err) {
		return rep, fmt.Errorf("%w: %v", ErrNoNetns, err)
	}
	return rep, err
}

func validateScratch(ctx context.Context, req ValidateRequest) (ValidateReport, error) {
	rewritten, services := scratchConfig(req.Config, req.ScratchPort)
	path, cleanup, err := writeCandidate(req.WorkDir, rewritten)
	if err != nil {
		return ValidateReport{}, err
	}
	defer cleanup()

	cmd := exec.Command(req.Bin, path)
	cmd.SysProcAttr = scratchSysProcAttr()

	rep, err := runAndWait(ctx, cmd, []int{req.ScratchPort}, services, req.Timeout)
	rep.Mode = ValidateScratch
	rep.Degraded = true
	rep.Note = ScratchCaveat
	if err != nil {
		return rep, fmt.Errorf("%w (%s)", err, ScratchCaveat)
	}
	return rep, nil
}

func scratchConfig(cfg []byte, scratch int) ([]byte, int) {
	services := 0
	out := make([]string, 0, 32)
	for _, raw := range strings.Split(string(cfg), "\n") {
		fields, _ := tokenize(strings.TrimRight(raw, "\r"))
		if len(fields) == 0 {
			out = append(out, raw)
			continue
		}
		switch fields[0] {
		case "internal":
			out = append(out, "internal 127.0.0.1")
		case ServiceProxy, ServiceSocks:
			services++
			parts := strings.Fields(raw)
			for i, p := range parts {
				if strings.HasPrefix(p, "-p") {
					parts[i] = "-p" + strconv.Itoa(scratch)
				}
			}
			out = append(out, strings.Join(parts, " "))
		default:
			out = append(out, raw)
		}
	}
	return []byte(strings.Join(out, "\n")), services
}

func writeCandidate(dir string, cfg []byte) (string, func(), error) {
	f, err := os.CreateTemp(dir, "dongled-validate-*.cfg")
	if err != nil {
		return "", func() {}, fmt.Errorf("%w: %v", ErrValidateFailed, err)
	}
	name := f.Name()
	cleanup := func() { os.Remove(name) }
	if _, err := f.Write(cfg); err != nil {
		f.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("%w: %v", ErrValidateFailed, err)
	}
	if err := f.Chmod(0o644); err != nil {
		f.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("%w: %v", ErrValidateFailed, err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("%w: %v", ErrValidateFailed, err)
	}
	return name, cleanup, nil
}

func runAndWait(ctx context.Context, cmd *exec.Cmd, ports []int, want int, timeout time.Duration) (ValidateReport, error) {
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stderr

	if err := cmd.Start(); err != nil {
		return ValidateReport{Stderr: stderr.String()}, fmt.Errorf("%w: start: %v", ErrValidateFailed, err)
	}

	pid := cmd.Process.Pid
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	defer func() {
		killProcessGroup(pid)
		cmd.Process.Kill()
		<-done
	}()

	deadline := time.Now().Add(timeout)
	procNet := fmt.Sprintf("/proc/%d/net/tcp", pid)
	for {
		bound, err := listeningPorts(procNet, ports)
		if err == nil && len(bound) >= want {
			return ValidateReport{BoundPorts: bound, Stderr: stderr.String()}, nil
		}
		select {
		case werr := <-done:
			done <- werr
			return ValidateReport{Stderr: stderr.String()},
				fmt.Errorf("%w: exited before binding %v: %s", ErrValidateFailed, ports, oneLine(stderr.String()))
		case <-ctx.Done():
			return ValidateReport{Stderr: stderr.String()}, ctx.Err()
		case <-time.After(validatePollInterval):
		}
		if time.Now().After(deadline) {
			return ValidateReport{Stderr: stderr.String()},
				fmt.Errorf("%w: no listener on %v after %s: %s", ErrValidateFailed, ports, timeout, oneLine(stderr.String()))
		}
	}
}

func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "(no output)"
	}
	return strings.Join(strings.Fields(s), " ")
}

func isPermissionError(err error) bool {
	return errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) ||
		errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.ENOSYS) ||
		strings.Contains(err.Error(), "operation not permitted")
}

func listeningPorts(procNet string, want []int) ([]int, error) {
	data, err := os.ReadFile(procNet)
	if err != nil {
		return nil, err
	}
	wanted := map[int]bool{}
	for _, p := range want {
		wanted[p] = true
	}
	out := []int{}
	for _, ap := range parseProcNetTCP(data) {
		if wanted[int(ap.Port())] {
			out = append(out, int(ap.Port()))
		}
	}
	return out, nil
}

func parseProcNetTCP(data []byte) []netip.AddrPort {
	out := []netip.AddrPort{}
	lines := strings.Split(string(data), "\n")
	for i, l := range lines {
		if i == 0 {
			continue
		}
		f := strings.Fields(l)
		if len(f) < 4 || f[3] != "0A" {
			continue
		}
		ap, ok := parseHexAddrPort(f[1])
		if !ok {
			continue
		}
		out = append(out, ap)
	}
	return out
}

func parseHexAddrPort(s string) (netip.AddrPort, bool) {
	host, port, ok := strings.Cut(s, ":")
	if !ok {
		return netip.AddrPort{}, false
	}
	p, err := strconv.ParseUint(port, 16, 16)
	if err != nil {
		return netip.AddrPort{}, false
	}
	raw, err := hex.DecodeString(host)
	if err != nil {
		return netip.AddrPort{}, false
	}
	switch len(raw) {
	case 4:
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], binary.BigEndian.Uint32(raw))
		return netip.AddrPortFrom(netip.AddrFrom4(b), uint16(p)), true
	case 16:
		var b [16]byte
		for i := 0; i < 16; i += 4 {
			binary.LittleEndian.PutUint32(b[i:i+4], binary.BigEndian.Uint32(raw[i:i+4]))
		}
		return netip.AddrPortFrom(netip.AddrFrom16(b), uint16(p)), true
	}
	return netip.AddrPort{}, false
}
