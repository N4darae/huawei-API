package proxysup

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net/netip"
	"strings"

	"github.com/n4darae/huawei-API/src/internal/config"
	"github.com/n4darae/huawei-API/src/internal/domain"
)

const (
	ServiceProxy = "proxy"
	ServiceSocks = "socks"

	AuthStrong = "strong"
	AuthIPOnly = "iponly"
	AuthNone   = "none"

	MaxConnFloor = 1
	MaxUsers     = 64
	MaxAuthIPs   = 256

	passwordAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
)

var (
	ErrBadTimeouts  = errors.New("render: timeouts line must carry exactly 10 values")
	ErrPortMismatch = errors.New("render: service ports must be derived from the slot")
	ErrNoLogFormat  = errors.New("render: logformat missing or does not log %e")
	ErrNoNServer    = errors.New("render: at least one nserver is required")
	ErrBadPassword  = errors.New("render: password must be exactly 16 characters of [A-Za-z0-9]")
	ErrBadUsername  = errors.New("render: username must be [A-Za-z0-9_.-]{1,63} and contain no ':'")
	ErrBadSlot      = errors.New("render: slot is out of range")
	ErrBadAddr      = errors.New("render: internal and external must both be IPv4 addresses")
	ErrBadPath      = errors.New("render: config path and log path are required")
	ErrBadAuthMode  = errors.New("render: unknown auth mode")
	ErrBadAuthIP    = errors.New("render: auth ip whitelist entry is not a valid prefix")
)

func NewSpec(slot domain.Slot, internal netip.Addr, nserverFallback netip.Addr) Spec {
	ns := []netip.Addr{slot.GatewayIP()}
	if nserverFallback.IsValid() && nserverFallback != slot.GatewayIP() {
		ns = append(ns, nserverFallback)
	}
	return Spec{
		Slot:       slot,
		InternalIP: internal,
		ExternalIP: slot.HostIP(),
		SocksPort:  slot.SocksPort(),
		HTTPPort:   slot.HTTPPort(),
		AuthMode:   domain.AuthUserPass,
		Policy:     domain.DefaultProxyPolicy(),
		NServers:   ns,
		LogPath:    slot.ProxyLogPath(),
		ConfigPath: slot.ProxyConfigPath(),
		UID:        slot.UID(),
		GID:        slot.GID(),
	}
}

func (sp Spec) Unit() string { return sp.Slot.ProxyUnit() }

func (sp Spec) UserName() string { return sp.Slot.UserName() }

func (sp Spec) Ports() []int { return []int{sp.HTTPPort, sp.SocksPort} }

func (sp Spec) ProbeUser() (string, string) {
	if len(sp.Users) == 0 {
		return "", ""
	}
	return sp.Users[0].Name, sp.Users[0].Password
}

func (sp Spec) Validate() error {
	if !sp.Slot.Valid() {
		return fmt.Errorf("%w: %d", ErrBadSlot, int(sp.Slot))
	}
	if !sp.InternalIP.IsValid() || !sp.InternalIP.Is4() || !sp.ExternalIP.IsValid() || !sp.ExternalIP.Is4() {
		return fmt.Errorf("%w: internal=%s external=%s", ErrBadAddr, sp.InternalIP, sp.ExternalIP)
	}
	if sp.SocksPort != sp.Slot.SocksPort() || sp.HTTPPort != sp.Slot.HTTPPort() {
		return fmt.Errorf("%w: slot %s wants socks=%d http=%d, got socks=%d http=%d",
			ErrPortMismatch, sp.Slot, sp.Slot.SocksPort(), sp.Slot.HTTPPort(), sp.SocksPort, sp.HTTPPort)
	}
	if sp.UID != sp.Slot.UID() || sp.GID != sp.Slot.GID() {
		return fmt.Errorf("%w: slot %s wants uid=%d gid=%d, got uid=%d gid=%d",
			ErrUIDMismatch, sp.Slot, sp.Slot.UID(), sp.Slot.GID(), sp.UID, sp.GID)
	}
	if sp.ConfigPath == "" || sp.LogPath == "" {
		return ErrBadPath
	}
	if !sp.AuthMode.Valid() {
		return fmt.Errorf("%w: %q", ErrBadAuthMode, string(sp.AuthMode))
	}
	if len(sp.NServers) == 0 {
		return ErrNoNServer
	}
	for _, ns := range sp.NServers {
		if !ns.IsValid() || !ns.Is4() {
			return fmt.Errorf("%w: nserver %s", ErrBadAddr, ns)
		}
	}
	if err := sp.validateUsers(); err != nil {
		return err
	}
	if err := sp.validateAuthIPs(); err != nil {
		return err
	}
	if err := sp.Policy.Validate(); err != nil {
		return err
	}
	if sp.Policy.MaxConn < MaxConnFloor {
		return fmt.Errorf("%w: maxconn %d", domain.ErrInvalid, sp.Policy.MaxConn)
	}
	return nil
}

func (sp Spec) validateUsers() error {
	if sp.AuthMode.UsesUserPass() && len(sp.Users) == 0 {
		return ErrNoUsers
	}
	if !sp.AuthMode.UsesUserPass() && len(sp.Users) > 0 {
		return ErrUsersUnused
	}
	if len(sp.Users) > MaxUsers {
		return fmt.Errorf("%w: %d users exceeds %d", domain.ErrInvalid, len(sp.Users), MaxUsers)
	}
	seen := make(map[string]struct{}, len(sp.Users))
	for _, u := range sp.Users {
		if !ValidUsername(u.Name) {
			return fmt.Errorf("%w: %q", ErrBadUsername, u.Name)
		}
		if !ValidPassword(u.Password) {
			return fmt.Errorf("%w: user %q", ErrBadPassword, u.Name)
		}
		if _, dup := seen[u.Name]; dup {
			return fmt.Errorf("%w: duplicate user %q", domain.ErrInvalid, u.Name)
		}
		seen[u.Name] = struct{}{}
	}
	return nil
}

func (sp Spec) validateAuthIPs() error {
	if sp.AuthMode.UsesIPList() && len(sp.AuthIPs) == 0 {
		return ErrNoAuthIPs
	}
	if !sp.AuthMode.UsesIPList() && len(sp.AuthIPs) > 0 {
		return fmt.Errorf("%w: auth mode %q ignores the ip whitelist", domain.ErrInvalid, string(sp.AuthMode))
	}
	if len(sp.AuthIPs) > MaxAuthIPs {
		return fmt.Errorf("%w: %d whitelist entries exceeds %d", domain.ErrInvalid, len(sp.AuthIPs), MaxAuthIPs)
	}
	for _, p := range sp.AuthIPs {
		if !p.IsValid() || !p.Addr().Is4() || p.Addr() != p.Masked().Addr() {
			return fmt.Errorf("%w: %s", ErrBadAuthIP, p)
		}
	}
	return nil
}

func ValidUsername(s string) bool {
	if s == "" || len(s) > 63 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.':
		default:
			return false
		}
	}
	return true
}

func ValidPassword(s string) bool {
	if len(s) != PasswordLen {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

func NewPassword() (string, error) {
	var b strings.Builder
	b.Grow(PasswordLen)
	max := big.NewInt(int64(len(passwordAlphabet)))
	for i := 0; i < PasswordLen; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b.WriteByte(passwordAlphabet[n.Int64()])
	}
	return b.String(), nil
}

func ValidatePort(p int) error {
	if p < config.ProxyPortLo || p > config.ProxyPortHi {
		return fmt.Errorf("%w: port %d outside %d-%d", ErrPortMismatch, p, config.ProxyPortLo, config.ProxyPortHi)
	}
	return nil
}
