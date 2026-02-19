package device

import (
	"context"
	"net/netip"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

type NetMode string

const (
	NetModeAuto NetMode = "auto"
	NetMode2G   NetMode = "2g"
	NetMode3G   NetMode = "3g"
	NetModeLTE  NetMode = "lte"
)

func AllNetModes() []NetMode {
	return []NetMode{NetModeAuto, NetMode2G, NetMode3G, NetModeLTE}
}

func (m NetMode) Valid() bool {
	for _, v := range AllNetModes() {
		if v == m {
			return true
		}
	}
	return false
}

type SMSBox int

const (
	SMSBoxInbox  SMSBox = 1
	SMSBoxOutbox SMSBox = 2
	SMSBoxDraft  SMSBox = 3
)

func (b SMSBox) Valid() bool { return b >= SMSBoxInbox && b <= SMSBoxDraft }

type SimState int

const (
	SimStateReady        SimState = 257
	SimStatePINRequired  SimState = 259
	SimStatePUKRequired  SimState = 260
	SimStatePINPUKLocked SimState = 261
)

func (s SimState) Ready() bool { return s == SimStateReady }

func (s SimState) Locked() bool {
	return s == SimStatePINRequired || s == SimStatePUKRequired || s == SimStatePINPUKLocked
}

type ConnStatus int

const (
	ConnConnecting    ConnStatus = 900
	ConnConnected     ConnStatus = 901
	ConnDisconnected  ConnStatus = 902
	ConnDisconnecting ConnStatus = 903
)

func (c ConnStatus) Connected() bool { return c == ConnConnected }

const (
	MaxIdleTimeDisabled = 0
	MaxIdleTimeDefault  = 300
)

const SMSTypeFragment = 2

type Info struct {
	DeviceName      string
	SerialNumber    string
	IMEI            string
	IMSI            string
	ICCID           string
	MSISDN          string
	HardwareVersion string
	SoftwareVersion string
	WebUIVersion    string
	MacAddress1     string
	MacAddress2     string
	ProductFamily   string
	Classify        string
	Uptime          int64
	WanIPAddress    netip.Addr
	WanDNSAddress   []netip.Addr
}

type Signal struct {
	RSSI   int
	RSRP   int
	RSRQ   int
	SINR   int
	Bars   int
	Band   string
	CellID string
	PLMN   string
	Mode   string
}

type Status struct {
	ConnectionStatus     ConnStatus
	SimStatus            int
	SignalStrength       int
	SignalIcon           int
	MaxSignal            int
	CurrentNetworkType   int
	CurrentNetworkTypeEx int
	ServiceStatus        int
	RoamingStatus        int
	WanIP                netip.Addr
}

type Traffic struct {
	CurrentConnectTime  int64
	CurrentUpload       int64
	CurrentDownload     int64
	CurrentUploadRate   int64
	CurrentDownloadRate int64
	TotalUpload         int64
	TotalDownload       int64
	TotalConnectTime    int64
}

type MonthStats struct {
	CurrentMonthUpload   int64
	CurrentMonthDownload int64
	MonthDuration        int64
	MonthLastClearTime   string
}

type DHCPSettings struct {
	DHCPIPAddress      netip.Addr
	DHCPLanNetmask     netip.Addr
	DHCPStatus         bool
	DHCPStartIPAddress netip.Addr
	DHCPEndIPAddress   netip.Addr
	DHCPLeaseTime      int
	DNSStatus          bool
	PrimaryDNS         netip.Addr
	SecondaryDNS       netip.Addr
}

type SMS struct {
	Index      int64
	Phone      string
	Content    string
	Date       int64
	Box        SMSBox
	Read       bool
	SmsType    int
	IsFragment bool
}

type Device interface {
	Information(ctx context.Context) (Info, error)
	Signal(ctx context.Context) (Signal, error)
	Status(ctx context.Context) (Status, error)
	DataSwitch(ctx context.Context, on bool) error
	SetMaxIdleTime(ctx context.Context, seconds int) error
	GetMaxIdleTime(ctx context.Context) (int, error)
	NetMode(ctx context.Context) (NetMode, error)
	SetNetMode(ctx context.Context, m NetMode) error
	Reboot(ctx context.Context) error
	DHCPSettings(ctx context.Context) (DHCPSettings, error)
	SetDHCPSettings(ctx context.Context, s DHCPSettings) error
	Traffic(ctx context.Context) (Traffic, error)
	MonthStats(ctx context.Context) (MonthStats, error)
	SMSList(ctx context.Context, box SMSBox, page, size int) ([]SMS, int, error)
	SMSSend(ctx context.Context, to []string, body string) error
	SMSDelete(ctx context.Context, index int64) error
	SMSSetRead(ctx context.Context, index int64) error
	PinStatus(ctx context.Context) (SimState, error)
	LoginRequired(ctx context.Context) (bool, error)
	Reachable(ctx context.Context) bool
}

type Registry interface {
	ForSlot(ctx context.Context, s domain.Slot) (Device, error)
	ForAddr(ctx context.Context, addr netip.Addr) (Device, error)
	Close() error
}

var FactoryDefaultAddr = netip.AddrFrom4([4]byte{192, 168, 8, 1})

var FactoryDefaultSubnet = netip.PrefixFrom(netip.AddrFrom4([4]byte{192, 168, 8, 0}), 24)
