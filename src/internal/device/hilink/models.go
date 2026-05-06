package hilink

import "encoding/xml"

const (
	PathDeviceInformation = "device/information"
	PathDeviceSignal      = "device/signal"
	PathDeviceControl     = "device/control"

	PathMonitoringStatus     = "monitoring/status"
	PathMonitoringTraffic    = "monitoring/traffic-statistics"
	PathMonitoringMonthStats = "monitoring/month_statistics"

	PathDialupDataSwitch = "dialup/mobile-dataswitch"
	PathDialupConnection = "dialup/connection"

	PathNetCurrentPLMN = "net/current-plmn"
	PathNetNetMode     = "net/net-mode"
	PathNetRegister    = "net/register"

	PathDHCPSettings = "dhcp/settings"

	PathSMSList    = "sms/sms-list"
	PathSMSSend    = "sms/send-sms"
	PathSMSDelete  = "sms/delete-sms"
	PathSMSSetRead = "sms/set-read"

	PathPinStatus = "pin/status"

	PathHiLinkLogin = "user/hilink_login"
)

const (
	ControlReboot = 1

	NetworkModeAuto  = "00"
	NetworkMode2G    = "01"
	NetworkMode3G    = "02"
	NetworkModeLTE   = "03"
	NetworkBandAll   = "3FFFFFFF"
	NetworkLTEAll    = "7FFFFFFFFFFFFFFF"
	SMSSendIndexNew  = -1
	SMSSendLengthNew = -1
	SMSSendDateNew   = "-1"
	SMSSendReserved  = 1

	SMSStatusNew = 0

	DefaultDHCPLeaseTime = 86400
)

type infoResponse struct {
	XMLName         xml.Name `xml:"response"`
	DeviceName      string   `xml:"DeviceName"`
	SerialNumber    string   `xml:"SerialNumber"`
	IMEI            string   `xml:"Imei"`
	IMSI            string   `xml:"Imsi"`
	ICCID           string   `xml:"Iccid"`
	MSISDN          string   `xml:"Msisdn"`
	HardwareVersion string   `xml:"HardwareVersion"`
	SoftwareVersion string   `xml:"SoftwareVersion"`
	WebUIVersion    string   `xml:"WebUIVersion"`
	MacAddress1     string   `xml:"MacAddress1"`
	MacAddress2     string   `xml:"MacAddress2"`
	ProductFamily   string   `xml:"ProductFamily"`
	Classify        string   `xml:"Classify"`
	SupportMode     string   `xml:"supportmode"`
	WorkMode        string   `xml:"workmode"`
	Uptime          string   `xml:"uptime"`
	WanIPAddress    string   `xml:"WanIPAddress"`
	WanDNSAddress   string   `xml:"wan_dns_address"`
}

type signalResponse struct {
	XMLName      xml.Name `xml:"response"`
	PCI          string   `xml:"pci"`
	SC           string   `xml:"sc"`
	CellID       string   `xml:"cell_id"`
	RSRQ         string   `xml:"rsrq"`
	RSRP         string   `xml:"rsrp"`
	RSSI         string   `xml:"rssi"`
	SINR         string   `xml:"sinr"`
	RSCP         string   `xml:"rscp"`
	ECIO         string   `xml:"ecio"`
	PSATT        string   `xml:"psatt"`
	Mode         string   `xml:"mode"`
	LTEBandwidth string   `xml:"lte_bandwidth"`
	LTEBandInfo  string   `xml:"lte_bandinfo"`
	Band         string   `xml:"band"`
	PLMN         string   `xml:"plmn"`
}

type statusResponse struct {
	XMLName              xml.Name `xml:"response"`
	ConnectionStatus     string   `xml:"ConnectionStatus"`
	WifiConnectionStatus string   `xml:"WifiConnectionStatus"`
	SignalStrength       string   `xml:"SignalStrength"`
	SignalIcon           string   `xml:"SignalIcon"`
	CurrentNetworkType   string   `xml:"CurrentNetworkType"`
	CurrentServiceDomain string   `xml:"CurrentServiceDomain"`
	RoamingStatus        string   `xml:"RoamingStatus"`
	SimlockStatus        string   `xml:"simlockStatus"`
	WanIPAddress         string   `xml:"WanIPAddress"`
	PrimaryDNS           string   `xml:"PrimaryDns"`
	SecondaryDNS         string   `xml:"SecondaryDns"`
	ServiceStatus        string   `xml:"ServiceStatus"`
	SimStatus            string   `xml:"SimStatus"`
	CurrentNetworkTypeEx string   `xml:"CurrentNetworkTypeEx"`
	MaxSignal            string   `xml:"maxsignal"`
	Classify             string   `xml:"classify"`
}

type trafficResponse struct {
	XMLName             xml.Name `xml:"response"`
	CurrentConnectTime  string   `xml:"CurrentConnectTime"`
	CurrentUpload       string   `xml:"CurrentUpload"`
	CurrentDownload     string   `xml:"CurrentDownload"`
	CurrentDownloadRate string   `xml:"CurrentDownloadRate"`
	CurrentUploadRate   string   `xml:"CurrentUploadRate"`
	TotalUpload         string   `xml:"TotalUpload"`
	TotalDownload       string   `xml:"TotalDownload"`
	TotalConnectTime    string   `xml:"TotalConnectTime"`
	ShowTraffic         string   `xml:"showtraffic"`
}

type monthStatsResponse struct {
	XMLName              xml.Name `xml:"response"`
	CurrentMonthDownload string   `xml:"CurrentMonthDownload"`
	CurrentMonthUpload   string   `xml:"CurrentMonthUpload"`
	MonthDuration        string   `xml:"MonthDuration"`
	MonthLastClearTime   string   `xml:"MonthLastClearTime"`
}

type dataSwitchResponse struct {
	XMLName    xml.Name `xml:"response"`
	DataSwitch string   `xml:"dataswitch"`
}

type dataSwitchRequest struct {
	XMLName    xml.Name `xml:"request"`
	DataSwitch string   `xml:"dataswitch"`
}

type connectionResponse struct {
	XMLName               xml.Name `xml:"response"`
	RoamAutoConnectEnable string   `xml:"RoamAutoConnectEnable"`
	MaxIdelTime           string   `xml:"MaxIdelTime"`
	ConnectMode           string   `xml:"ConnectMode"`
	MTU                   string   `xml:"MTU"`
	AutoDialSwitch        string   `xml:"auto_dial_switch"`
	PdpAlwaysOn           string   `xml:"pdp_always_on"`
}

type connectionRequest struct {
	XMLName               xml.Name `xml:"request"`
	RoamAutoConnectEnable string   `xml:"RoamAutoConnectEnable"`
	MaxIdelTime           string   `xml:"MaxIdelTime"`
	ConnectMode           string   `xml:"ConnectMode"`
	MTU                   string   `xml:"MTU"`
	AutoDialSwitch        string   `xml:"auto_dial_switch"`
	PdpAlwaysOn           string   `xml:"pdp_always_on"`
}

type plmnResponse struct {
	XMLName   xml.Name `xml:"response"`
	State     string   `xml:"State"`
	FullName  string   `xml:"FullName"`
	ShortName string   `xml:"ShortName"`
	Numeric   string   `xml:"Numeric"`
	Rat       string   `xml:"Rat"`
}

type netModeResponse struct {
	XMLName     xml.Name `xml:"response"`
	NetworkMode string   `xml:"NetworkMode"`
	NetworkBand string   `xml:"NetworkBand"`
	LTEBand     string   `xml:"LTEBand"`
}

type netModeRequest struct {
	XMLName     xml.Name `xml:"request"`
	NetworkMode string   `xml:"NetworkMode"`
	NetworkBand string   `xml:"NetworkBand"`
	LTEBand     string   `xml:"LTEBand"`
}

type registerRequest struct {
	XMLName xml.Name `xml:"request"`
	Mode    string   `xml:"Mode"`
	Plmn    string   `xml:"Plmn"`
	Rat     string   `xml:"Rat"`
}

type controlRequest struct {
	XMLName xml.Name `xml:"request"`
	Control string   `xml:"Control"`
}

type dhcpResponse struct {
	XMLName            xml.Name `xml:"response"`
	DNSStatus          string   `xml:"DnsStatus"`
	DHCPStartIPAddress string   `xml:"DhcpStartIPAddress"`
	DHCPIPAddress      string   `xml:"DhcpIPAddress"`
	AccessIPAddress    string   `xml:"accessipaddress"`
	HomeURL            string   `xml:"homeurl"`
	DHCPStatus         string   `xml:"DhcpStatus"`
	DHCPLanNetmask     string   `xml:"DhcpLanNetmask"`
	SecondaryDNS       string   `xml:"SecondaryDns"`
	PrimaryDNS         string   `xml:"PrimaryDns"`
	DHCPEndIPAddress   string   `xml:"DhcpEndIPAddress"`
	DHCPLeaseTime      string   `xml:"DhcpLeaseTime"`
	ShowDNSSetting     string   `xml:"ShowDnsSetting"`
}

type dhcpRequest struct {
	XMLName            xml.Name `xml:"request"`
	DHCPIPAddress      string   `xml:"DhcpIPAddress"`
	DHCPLanNetmask     string   `xml:"DhcpLanNetmask"`
	DHCPStatus         string   `xml:"DhcpStatus"`
	DHCPStartIPAddress string   `xml:"DhcpStartIPAddress"`
	DHCPEndIPAddress   string   `xml:"DhcpEndIPAddress"`
	DHCPLeaseTime      string   `xml:"DhcpLeaseTime"`
	DNSStatus          string   `xml:"DnsStatus"`
	PrimaryDNS         string   `xml:"PrimaryDns"`
	SecondaryDNS       string   `xml:"SecondaryDns"`
	ShowDNSSetting     string   `xml:"ShowDnsSetting"`
}

type smsMessage struct {
	XMLName  xml.Name `xml:"Message"`
	Smstat   string   `xml:"Smstat"`
	Index    string   `xml:"Index"`
	Phone    string   `xml:"Phone"`
	Content  string   `xml:"Content"`
	Date     string   `xml:"Date"`
	Sca      string   `xml:"Sca"`
	SaveType string   `xml:"SaveType"`
	Priority string   `xml:"Priority"`
	SmsType  string   `xml:"SmsType"`
}

type smsMessages struct {
	XMLName  xml.Name     `xml:"Messages"`
	Messages []smsMessage `xml:"Message"`
}

type smsListResponse struct {
	XMLName  xml.Name    `xml:"response"`
	Count    string      `xml:"Count"`
	Messages smsMessages `xml:"Messages"`
}

type smsListRequest struct {
	XMLName         xml.Name `xml:"request"`
	PageIndex       string   `xml:"PageIndex"`
	ReadCount       string   `xml:"ReadCount"`
	BoxType         string   `xml:"BoxType"`
	SortType        string   `xml:"SortType"`
	Ascending       string   `xml:"Ascending"`
	UnreadPreferred string   `xml:"UnreadPreferred"`
}

type smsPhones struct {
	XMLName xml.Name `xml:"Phones"`
	Phone   []string `xml:"Phone"`
}

type smsSendRequest struct {
	XMLName  xml.Name  `xml:"request"`
	Index    string    `xml:"Index"`
	Phones   smsPhones `xml:"Phones"`
	Sca      string    `xml:"Sca"`
	Content  string    `xml:"Content"`
	Length   string    `xml:"Length"`
	Reserved string    `xml:"Reserved"`
	Date     string    `xml:"Date"`
}

type smsIndexRequest struct {
	XMLName xml.Name `xml:"request"`
	Index   string   `xml:"Index"`
}

type pinStatusResponse struct {
	XMLName     xml.Name `xml:"response"`
	SimState    string   `xml:"SimState"`
	PinOptState string   `xml:"PinOptState"`
	SimPinTimes string   `xml:"SimPinTimes"`
	SimPukTimes string   `xml:"SimPukTimes"`
}

type hilinkLoginResponse struct {
	XMLName      xml.Name `xml:"response"`
	HilinkLogin  string   `xml:"hilink_login"`
	Password     string   `xml:"password"`
	Username     string   `xml:"username"`
	PasswordType string   `xml:"password_type"`
}
