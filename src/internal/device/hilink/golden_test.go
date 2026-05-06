package hilink

import (
	"io/fs"
	"testing"

	"github.com/n4darae/huawei-API/src/internal/device"
)

func TestFixturesPresent(t *testing.T) {
	entries, err := fs.ReadDir(Fixtures(), ".")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range entries {
		got[e.Name()] = true
	}
	want := []string{
		"webserver_sestokinfo.xml",
		"device_information.xml",
		"device_basic_information.xml",
		"device_signal.xml",
		"device_signal_e3372.xml",
		"monitoring_status.xml",
		"monitoring_status_wanip.xml",
		"monitoring_traffic_statistics.xml",
		"monitoring_month_statistics.xml",
		"dialup_connection.xml",
		"dialup_mobile_dataswitch.xml",
		"net_current_plmn.xml",
		"net_register.xml",
		"net_net_mode.xml",
		"pin_status.xml",
		"sms_sms_list.xml",
		"sms_sms_list_empty.xml",
		"sms_sms_list_fragment.xml",
		"dhcp_settings.xml",
		"response_ok.xml",
		"user_hilink_login.xml",
		"error_100002.xml",
		"error_100003.xml",
		"error_100004.xml",
		"error_100005.xml",
		"error_100010.xml",
		"error_108003.xml",
		"error_112001.xml",
		"error_125002.xml",
		"error_125003.xml",
		"error_125003_compact.xml",
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("missing fixture %s", name)
		}
	}
	success := 0
	for _, name := range want {
		if rootElement(Fixture(name)) == "response" {
			success++
		}
	}
	if success < 12 {
		t.Fatalf("only %d success fixtures, need at least 12", success)
	}
}

func TestGoldenDeviceInformation(t *testing.T) {
	var r infoResponse
	if err := Unmarshal(Fixture("device_information.xml"), &r); err != nil {
		t.Fatal(err)
	}
	if r.DeviceName != "E3372" {
		t.Errorf("DeviceName = %q", r.DeviceName)
	}
	if r.IMEI != "861821032479591" {
		t.Errorf("Imei = %q", r.IMEI)
	}
	if r.ICCID != "8999701560257048991F" {
		t.Errorf("Iccid = %q", r.ICCID)
	}
	if r.HardwareVersion != "CL2E3372HM" {
		t.Errorf("HardwareVersion = %q", r.HardwareVersion)
	}
	if r.MSISDN != "" {
		t.Errorf("Msisdn should be empty, got %q", r.MSISDN)
	}
}

func TestGoldenSignalUnitSuffixes(t *testing.T) {
	var r signalResponse
	if err := Unmarshal(Fixture("device_signal.xml"), &r); err != nil {
		t.Fatal(err)
	}
	if got := suffixInt(r.RSSI); got != -89 {
		t.Errorf("rssi = %d", got)
	}
	if got := suffixInt(r.RSRP); got != -102 {
		t.Errorf("rsrp = %d", got)
	}
	if got := suffixInt(r.RSRQ); got != -6 {
		t.Errorf("rsrq = %d", got)
	}
	if got := suffixInt(r.SINR); got != 3 {
		t.Errorf("sinr = %d", got)
	}
	if r.CellID != "551" {
		t.Errorf("cell_id = %q", r.CellID)
	}
}

func TestGoldenSignalAllEmpty(t *testing.T) {
	var r signalResponse
	if err := Unmarshal(Fixture("device_signal_e3372.xml"), &r); err != nil {
		t.Fatal(err)
	}
	if suffixInt(r.RSSI) != 0 || suffixInt(r.RSRQ) != 0 {
		t.Fatalf("empty signal fields must decode to zero, got %+v", r)
	}
	if r.Mode != "2" {
		t.Errorf("mode = %q", r.Mode)
	}
}

func TestGoldenMonitoringStatusBothVariants(t *testing.T) {
	var withIP statusResponse
	if err := Unmarshal(Fixture("monitoring_status_wanip.xml"), &withIP); err != nil {
		t.Fatal(err)
	}
	if atoi(withIP.ConnectionStatus) != int(device.ConnConnected) {
		t.Errorf("ConnectionStatus = %q", withIP.ConnectionStatus)
	}
	if got := parseAddr(withIP.WanIPAddress).String(); got != "10.115.89.118" {
		t.Errorf("WanIPAddress = %q", got)
	}

	var noIP statusResponse
	if err := Unmarshal(Fixture("monitoring_status.xml"), &noIP); err != nil {
		t.Fatal(err)
	}
	if noIP.WanIPAddress != "" {
		t.Errorf("firmware without WanIPAddress must decode to empty, got %q", noIP.WanIPAddress)
	}
	if parseAddr(noIP.WanIPAddress).IsValid() {
		t.Error("absent WanIPAddress must not yield a valid addr")
	}
	if atoi(noIP.ConnectionStatus) != int(device.ConnConnected) {
		t.Errorf("ConnectionStatus = %q", noIP.ConnectionStatus)
	}
}

func TestGoldenTraffic(t *testing.T) {
	var r trafficResponse
	if err := Unmarshal(Fixture("monitoring_traffic_statistics.xml"), &r); err != nil {
		t.Fatal(err)
	}
	if atoi64(r.CurrentDownload) != 11407740 {
		t.Errorf("CurrentDownload = %q", r.CurrentDownload)
	}
	if atoi64(r.TotalConnectTime) != 3348 {
		t.Errorf("TotalConnectTime = %q", r.TotalConnectTime)
	}
}

func TestGoldenMonthStats(t *testing.T) {
	var r monthStatsResponse
	if err := Unmarshal(Fixture("monitoring_month_statistics.xml"), &r); err != nil {
		t.Fatal(err)
	}
	if atoi64(r.CurrentMonthDownload) != 21078592898 {
		t.Errorf("CurrentMonthDownload = %q", r.CurrentMonthDownload)
	}
	if r.MonthLastClearTime != "2015-4-28" {
		t.Errorf("MonthLastClearTime must stay verbatim, got %q", r.MonthLastClearTime)
	}
}

func TestGoldenDialupConnectionMisspelling(t *testing.T) {
	var r connectionResponse
	if err := Unmarshal(Fixture("dialup_connection.xml"), &r); err != nil {
		t.Fatal(err)
	}
	if atoi(r.MaxIdelTime) != 600 {
		t.Fatalf("MaxIdelTime = %q, want 600 seconds", r.MaxIdelTime)
	}
	if r.MTU != "1500" {
		t.Errorf("MTU = %q", r.MTU)
	}
}

func TestGoldenPLMNAndNetMode(t *testing.T) {
	var p plmnResponse
	if err := Unmarshal(Fixture("net_current_plmn.xml"), &p); err != nil {
		t.Fatal(err)
	}
	if p.FullName != "Beeline KZ" || p.Numeric != "40101" {
		t.Errorf("plmn = %+v", p)
	}

	var n netModeResponse
	if err := Unmarshal(Fixture("net_net_mode.xml"), &n); err != nil {
		t.Fatal(err)
	}
	m, ok := NetModeFromCode(n.NetworkMode)
	if !ok || m != device.NetMode2G {
		t.Fatalf("NetworkMode %q -> %v %v", n.NetworkMode, m, ok)
	}
}

func TestGoldenPinStatus(t *testing.T) {
	var r pinStatusResponse
	if err := Unmarshal(Fixture("pin_status.xml"), &r); err != nil {
		t.Fatal(err)
	}
	if atoi(r.SimState) != SimStateReady {
		t.Errorf("SimState = %q", r.SimState)
	}
	if atoi(r.SimPukTimes) != 10 {
		t.Errorf("SimPukTimes = %q", r.SimPukTimes)
	}
}

func TestGoldenDHCPSettings(t *testing.T) {
	var r dhcpResponse
	if err := Unmarshal(Fixture("dhcp_settings.xml"), &r); err != nil {
		t.Fatal(err)
	}
	if r.DHCPIPAddress != "192.168.8.1" {
		t.Errorf("DhcpIPAddress = %q", r.DHCPIPAddress)
	}
	if atoi(r.DHCPLeaseTime) != 86400 {
		t.Errorf("DhcpLeaseTime = %q", r.DHCPLeaseTime)
	}
	if r.HomeURL != "hi.link" {
		t.Errorf("homeurl = %q", r.HomeURL)
	}
	if !isSet(r.DNSStatus) || !isSet(r.DHCPStatus) {
		t.Errorf("status flags = %q %q", r.DNSStatus, r.DHCPStatus)
	}
}

func TestGoldenSMSList(t *testing.T) {
	var r smsListResponse
	if err := Unmarshal(Fixture("sms_sms_list.xml"), &r); err != nil {
		t.Fatal(err)
	}
	if atoi(r.Count) != 2 || len(r.Messages.Messages) != 2 {
		t.Fatalf("count %q, messages %d", r.Count, len(r.Messages.Messages))
	}
	first := smsFrom(r.Messages.Messages[0], device.SMSBoxInbox)
	if first.Index != 40003 || first.Phone != "+48616673870" {
		t.Errorf("first = %+v", first)
	}
	if first.Read {
		t.Error("Smstat 0 means unread")
	}
	if first.IsFragment {
		t.Error("SmsType 1 is not a fragment")
	}
	second := smsFrom(r.Messages.Messages[1], device.SMSBoxInbox)
	if !second.Read {
		t.Error("Smstat 1 means read")
	}
}

func TestGoldenSMSFragmentFlag(t *testing.T) {
	var r smsListResponse
	if err := Unmarshal(Fixture("sms_sms_list_fragment.xml"), &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Messages.Messages) != 1 {
		t.Fatalf("messages = %d", len(r.Messages.Messages))
	}
	m := smsFrom(r.Messages.Messages[0], device.SMSBoxInbox)
	if m.SmsType != device.SMSTypeFragment {
		t.Fatalf("SmsType = %d", m.SmsType)
	}
	if !m.IsFragment {
		t.Fatal("SmsType 2 must set IsFragment")
	}
}

func TestGoldenSMSListEmpty(t *testing.T) {
	var r smsListResponse
	if err := Unmarshal(Fixture("sms_sms_list_empty.xml"), &r); err != nil {
		t.Fatal(err)
	}
	if atoi(r.Count) != 0 || len(r.Messages.Messages) != 0 {
		t.Fatalf("empty inbox decoded as %q / %d", r.Count, len(r.Messages.Messages))
	}
}

func TestGoldenSesTokInfo(t *testing.T) {
	var r sesTokInfo
	if err := Unmarshal(Fixture("webserver_sestokinfo.xml"), &r); err != nil {
		t.Fatal(err)
	}
	if r.TokInfo != "N6aUzSFsKRXnrTJgcL482NaKqsO+PRF7" {
		t.Errorf("TokInfo = %q", r.TokInfo)
	}
	if normalizeCookie(r.SesInfo) != "SessionID=..." {
		t.Errorf("SesInfo = %q", r.SesInfo)
	}
}

func TestSMSDateParsing(t *testing.T) {
	if ParseSMSDate("2025-06-09 17:08:58") == 0 {
		t.Fatal("valid date must parse")
	}
	if ParseSMSDate("") != 0 {
		t.Fatal("empty date must be zero")
	}
	if ParseSMSDate("nonsense") != 0 {
		t.Fatal("unparseable date must be zero")
	}
}
