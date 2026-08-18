package domain

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/n4darae/huawei-API/src/internal/config"
)

const (
	// PROVISIONAL: pending A2/A3 hardware measurement
	MaxSlots = 150
	// PROVISIONAL: pending A2/A3 hardware measurement
	IfacePrefix = "dg"
	// PROVISIONAL: pending A2/A3 hardware measurement
	SubnetOctetBase = 100
	// PROVISIONAL: pending A2/A3 hardware measurement
	RouteTableBase = 1000
	// PROVISIONAL: pending A2/A3 hardware measurement
	RulePrioSrc = 1000
	// PROVISIONAL: pending A2/A3 hardware measurement
	RulePrioUID = 1500

	RulePrioPublic  = 900
	UIDBase         = 6100
	ForeignRuleCeil = 5210
	UserPrefix      = "px"
	SubnetBits      = 24
	GatewayOctet    = 1
	HostOctet       = 100
)

type Slot int

func Slots() []Slot {
	out := make([]Slot, 0, MaxSlots)
	for i := 1; i <= MaxSlots; i++ {
		out = append(out, Slot(i))
	}
	return out
}

func (s Slot) Valid() bool { return s >= 1 && s <= MaxSlots }

func (s Slot) Int() int { return int(s) }

func (s Slot) String() string { return fmt.Sprintf("%02d", int(s)) }

func (s Slot) IfaceName() string { return fmt.Sprintf("%s%02d", IfacePrefix, int(s)) }

func (s Slot) UserName() string { return fmt.Sprintf("%s%02d", UserPrefix, int(s)) }

func (s Slot) UID() int { return UIDBase + int(s) }

func (s Slot) GID() int { return config.GroupGID }

func (s Slot) Subnet() netip.Prefix {
	return netip.PrefixFrom(s.addr(0), SubnetBits)
}

func (s Slot) GatewayIP() netip.Addr { return s.addr(GatewayOctet) }

func (s Slot) HostIP() netip.Addr { return s.addr(HostOctet) }

func (s Slot) HostPrefix() netip.Prefix { return netip.PrefixFrom(s.HostIP(), SubnetBits) }

func (s Slot) addr(last byte) netip.Addr {
	return netip.AddrFrom4([4]byte{192, 168, byte(SubnetOctetBase + int(s)), last})
}

func (s Slot) RouteTable() int { return RouteTableBase + int(s) }

func (s Slot) RulePrioSrc() int { return RulePrioSrc + int(s) }

func (s Slot) RulePrioUID() int { return RulePrioUID + int(s) }

func (s Slot) SocksPort() int { return config.SocksPortBase + int(s) }

func (s Slot) HTTPPort() int { return config.HTTPPortBase + int(s) }

func (s Slot) LinkFileName() string { return fmt.Sprintf("10-%s-%02d.link", config.Product, int(s)) }

func (s Slot) NetworkFileName() string {
	return fmt.Sprintf("70-%s-%02d.network", config.Product, int(s))
}

func (s Slot) RouteTableName() string { return fmt.Sprintf("%s%02d", config.Product, int(s)) }

func (s Slot) ProxyUnit() string { return config.ProxyUnit(s.UserName()) }

func (s Slot) ProxyConfigPath() string { return config.ProxyConfigPath(s.UserName()) }

func (s Slot) ProxyLogPath() string { return config.ProxyLogPath(s.UserName()) }

func ParseIfaceName(name string) (Slot, bool) {
	if !strings.HasPrefix(name, IfacePrefix) {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(name, IfacePrefix))
	if err != nil {
		return 0, false
	}
	s := Slot(n)
	if !s.Valid() {
		return 0, false
	}
	return s, true
}

func SlotFromUID(uid int) (Slot, bool) {
	s := Slot(uid - UIDBase)
	if !s.Valid() {
		return 0, false
	}
	return s, true
}
