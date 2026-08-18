package sim

import (
	"fmt"
	"net/netip"
	"strconv"
	"time"

	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/domain"
)

const (
	DefaultHoldToNewIP = 10 * time.Second
	DefaultCarrier     = "Beeline KZ"
	DefaultPLMN        = "40101"
	DefaultRat         = "2"
	DefaultMTU         = "1500"
	DefaultNetworkBand = "3FFFFFFF"
	DefaultLTEBand     = "7FFFFFFFFFFFFFFF"
	DefaultLeaseTime   = 86400
	DefaultMaxSignal   = 5

	SMSDateLayout = "2006-01-02 15:04:05"
)

type Message struct {
	Index    int64
	Phone    string
	Content  string
	Date     string
	Smstat   int
	SaveType int
	Priority int
	SmsType  int
	Box      device.SMSBox
}

type state struct {
	slot domain.Slot

	deviceName      string
	serialNumber    string
	imei            string
	imsi            string
	iccid           string
	msisdn          string
	hardwareVersion string
	softwareVersion string
	webUIVersion    string
	macAddress1     string

	carrier string
	plmn    string
	rat     string

	publicIP  netip.Addr
	ipCounter uint32

	dataOn       bool
	dataOffAt    time.Time
	connectedAt  time.Time
	lastActivity time.Time
	wedgeUntil   time.Time

	maxIdleTime int
	connectMode string
	roamAuto    string
	mtu         string
	pdpAlwaysOn string

	netMode     device.NetMode
	networkBand string
	lteBand     string

	simState    device.SimState
	pinOptState int
	simPinTimes int
	simPukTimes int

	hilinkLogin bool

	dhcp device.DHCPSettings

	messages  []Message
	nextIndex int64

	totalUpload      int64
	totalDownload    int64
	currentUpload    int64
	currentDownload  int64
	monthUpload      int64
	monthDownload    int64
	monthDuration    int64
	monthLastClear   string
	totalConnectTime int64
}

func newState(slot domain.Slot, carrier string) *state {
	s := &state{
		slot:            slot,
		deviceName:      "E3372",
		serialNumber:    "G4PDW16623003" + pad3(slot.Int()),
		imei:            "861821032479" + pad3(slot.Int()),
		imsi:            "401015625704" + pad3(slot.Int()),
		iccid:           "8999701560257048991F",
		msisdn:          "",
		hardwareVersion: "CL2E3372HM",
		softwareVersion: "22.317.01.00.00",
		webUIVersion:    "17.100.14.02.577",
		macAddress1:     "BA:AB:BE:34:00:" + macOctet(slot.Int()),
		carrier:         carrier,
		plmn:            DefaultPLMN,
		rat:             DefaultRat,
		maxIdleTime:     device.MaxIdleTimeDefault,
		connectMode:     "0",
		roamAuto:        "0",
		mtu:             DefaultMTU,
		pdpAlwaysOn:     "0",
		netMode:         device.NetModeAuto,
		networkBand:     DefaultNetworkBand,
		lteBand:         DefaultLTEBand,
		simState:        device.SimStateReady,
		pinOptState:     258,
		simPinTimes:     3,
		simPukTimes:     10,
		nextIndex:       40000,
		monthLastClear:  "2015-4-28",
	}
	s.dhcp = device.DHCPSettings{
		DHCPIPAddress:      device.FactoryDefaultAddr,
		DHCPLanNetmask:     netip.AddrFrom4([4]byte{255, 255, 255, 0}),
		DHCPStatus:         true,
		DHCPStartIPAddress: netip.AddrFrom4([4]byte{192, 168, 8, 100}),
		DHCPEndIPAddress:   netip.AddrFrom4([4]byte{192, 168, 8, 200}),
		DHCPLeaseTime:      DefaultLeaseTime,
		DNSStatus:          true,
		PrimaryDNS:         device.FactoryDefaultAddr,
		SecondaryDNS:       device.FactoryDefaultAddr,
	}
	s.ipCounter = uint32(slot.Int()) << 8
	s.publicIP = carrierIP(s.ipCounter)
	return s
}

func carrierIP(n uint32) netip.Addr {
	v := n % (254 * 254)
	return netip.AddrFrom4([4]byte{10, 115, byte(v/254 + 1), byte(v%254 + 1)})
}

func (s *state) rotateIP() {
	s.ipCounter++
	s.publicIP = carrierIP(s.ipCounter)
}

func (s *state) connection(now time.Time) device.ConnStatus {
	if !s.dataOn {
		return device.ConnDisconnected
	}
	if now.Before(s.wedgeUntil) {
		return device.ConnConnecting
	}
	if s.maxIdleTime > 0 && !s.lastActivity.IsZero() {
		idle := now.Sub(s.lastActivity)
		if idle >= time.Duration(s.maxIdleTime)*time.Second {
			return device.ConnDisconnected
		}
	}
	return device.ConnConnected
}

func (s *state) wanIP(now time.Time) netip.Addr {
	if s.connection(now) != device.ConnConnected {
		return netip.Addr{}
	}
	return s.publicIP
}

func (s *state) connectTime(now time.Time) int64 {
	if s.connectedAt.IsZero() || s.connection(now) != device.ConnConnected {
		return 0
	}
	return int64(now.Sub(s.connectedAt) / time.Second)
}

// pad3 formats a slot as exactly three digits. Slots run to domain.MaxSlots (150), so
// two digits cannot hold them: the previous n%100 wrapped slot 150 onto slot 50 and gave
// both simulated dongles the same identity. Three digits keeps every slot distinct while
// the concatenated serial/IMEI/IMSI stay their required fixed lengths, because the
// prefixes above lost one character each.
func pad3(n int) string { return fmt.Sprintf("%03d", n) }

// macOctet formats a slot as one MAC octet. The last octet must be exactly two hex
// characters, and 1..150 fits in a single octet, so hex holds the width without wrapping.
func macOctet(n int) string { return fmt.Sprintf("%02X", n) }

func itoa(n int) string { return strconv.Itoa(n) }

func bit(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func addrOrEmpty(a netip.Addr) string {
	if !a.IsValid() {
		return ""
	}
	return a.String()
}
