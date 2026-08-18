package sim

import (
	"bytes"
	"encoding/xml"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/device/hilink"
	"github.com/n4darae/huawei-API/src/internal/domain"
)

const DefaultMaxHang = 30 * time.Second

const (
	maxSMSPageSize  = 200
	maxSMSPageIndex = 10000
)

type SimDevice struct {
	mu sync.Mutex

	st     *state
	tok    *tokenStore
	faults *faultInjector

	now         func() time.Time
	holdToNewIP time.Duration
	maxHang     time.Duration

	omitWanIP  bool
	unreadable bool

	dhcpMoved bool
	onMove    func(old, cur netip.Addr, d *SimDevice)
}

func (d *SimDevice) Slot() domain.Slot {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.st.slot
}

func (d *SimDevice) LANAddr() netip.Addr {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.st.dhcp.DHCPIPAddress
}

func (d *SimDevice) PublicIP() netip.Addr {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.st.publicIP
}

func (d *SimDevice) SetPublicIP(ip netip.Addr) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.st.publicIP = ip
}

func (d *SimDevice) Wedge(dur time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.st.wedgeUntil = d.now().Add(dur)
}

func (d *SimDevice) SetPinLocked(s device.SimState) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.st.simState = s
}

func (d *SimDevice) SetHiLinkLogin(on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.st.hilinkLogin = on
}

func (d *SimDevice) SetOmitWanIP(on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.omitWanIP = on
}

func (d *SimDevice) SetUnreachable(on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.unreadable = on
}

func (d *SimDevice) SetMaxIdleTime(seconds int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.st.maxIdleTime = seconds
}

func (d *SimDevice) MaxIdleTime() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.st.maxIdleTime
}

func (d *SimDevice) MarkActivity() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.st.lastActivity = d.now()
}

func (d *SimDevice) ConnectionStatus() device.ConnStatus {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.st.connection(d.now())
}

func (d *SimDevice) DataOn() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.st.dataOn
}

func (d *SimDevice) Faults() *faultInjector { return d.faults }

func (d *SimDevice) AddMessage(m Message) int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	if m.Index == 0 {
		m.Index = d.st.nextIndex
		d.st.nextIndex++
	}
	if m.Date == "" {
		m.Date = d.now().Format(SMSDateLayout)
	}
	if m.Box == 0 {
		m.Box = device.SMSBoxInbox
	}
	if m.SmsType == 0 {
		m.SmsType = 1
	}
	d.st.messages = append(d.st.messages, m)
	return m.Index
}

func (d *SimDevice) Messages() []Message {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Message, len(d.st.messages))
	copy(out, d.st.messages)
	return out
}

func (d *SimDevice) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/")
	path = strings.Trim(path, "/")

	d.mu.Lock()
	unreadable := d.unreadable
	d.mu.Unlock()
	if unreadable {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	switch d.faults.next() {
	case FaultTimeout:
		d.hang(r)
		return
	case FaultReenumerate:
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	case FaultSlowResponse:
		if !d.pause(r, d.faults.slowFor()) {
			return
		}
	case FaultErr100002:
		writeAPIError(w, "", domain.CodeSystemNoSupport)
		return
	case FaultErr112001:
		writeAPIError(w, "", domain.CodeSetNetModeWhenDialup)
		return
	case FaultTokenExpire:
		d.tok.expireAll()
	}

	if path == hilink.PathSesTokInfo {
		cookie, tokens := d.tok.handshake()
		body := rewriteElements(hilink.Fixture("webserver_sestokinfo.xml"), map[string]string{
			"SesInfo": cookie,
			"TokInfo": strings.Join(tokens, hilink.TokenSeparator),
		})
		writeXML(w, body, cookie, nil)
		return
	}

	cookie := sessionCookie(r)
	if !d.tok.sessionValid(cookie) {
		writeAPIError(w, "", domain.CodeWrongSessionToken)
		return
	}

	if r.Method == http.MethodPost {
		token := r.Header.Get(hilink.HeaderToken)
		if !d.tok.consume(token) {
			writeAPIError(w, "", domain.CodeSystemCSRF)
			return
		}
		d.mu.Lock()
		login := d.st.hilinkLogin
		d.mu.Unlock()
		if login {
			writeAPIError(w, "", domain.CodeSystemNoRights)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeAPIError(w, "", domain.CodeFormatError)
			return
		}
		d.post(w, r, path, parseRequest(body))
		return
	}

	d.get(w, path)
}

func (d *SimDevice) get(w http.ResponseWriter, path string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()

	switch path {
	case hilink.PathDeviceInformation:
		writeXML(w, rewriteElements(hilink.Fixture("device_information.xml"), map[string]string{
			"DeviceName":      d.st.deviceName,
			"SerialNumber":    d.st.serialNumber,
			"Imei":            d.st.imei,
			"Imsi":            d.st.imsi,
			"Iccid":           d.st.iccid,
			"Msisdn":          d.st.msisdn,
			"HardwareVersion": d.st.hardwareVersion,
			"SoftwareVersion": d.st.softwareVersion,
			"WebUIVersion":    d.st.webUIVersion,
			"MacAddress1":     d.st.macAddress1,
		}), "", nil)

	case hilink.PathDeviceSignal:
		writeXML(w, rewriteElements(hilink.Fixture("device_signal.xml"), map[string]string{
			"cell_id": "551",
			"rsrq":    "-6dB",
			"rsrp":    "-102dBm",
			"rssi":    "-89dBm",
			"sinr":    "3dB",
			"mode":    "7",
		}), "", nil)

	case hilink.PathMonitoringStatus:
		src := hilink.Fixture("monitoring_status_wanip.xml")
		if d.omitWanIP {
			src = hilink.Fixture("monitoring_status.xml")
		}
		writeXML(w, rewriteElements(src, map[string]string{
			"ConnectionStatus":     itoa(int(d.st.connection(now))),
			"SignalIcon":           "5",
			"CurrentNetworkType":   "19",
			"CurrentNetworkTypeEx": "101",
			"RoamingStatus":        "0",
			"WanIPAddress":         addrOrEmpty(d.st.wanIP(now)),
			"ServiceStatus":        "2",
			"SimStatus":            bit(d.st.simState == device.SimStateReady),
			"maxsignal":            itoa(DefaultMaxSignal),
			"classify":             "hilink",
		}), "", nil)

	case hilink.PathMonitoringTraffic:
		writeXML(w, rewriteElements(hilink.Fixture("monitoring_traffic_statistics.xml"), map[string]string{
			"CurrentConnectTime":  i64(d.st.connectTime(now)),
			"CurrentUpload":       i64(d.st.currentUpload),
			"CurrentDownload":     i64(d.st.currentDownload),
			"CurrentDownloadRate": "0",
			"CurrentUploadRate":   "0",
			"TotalUpload":         i64(d.st.totalUpload),
			"TotalDownload":       i64(d.st.totalDownload),
			"TotalConnectTime":    i64(d.st.totalConnectTime),
		}), "", nil)

	case hilink.PathMonitoringMonthStats:
		writeXML(w, rewriteElements(hilink.Fixture("monitoring_month_statistics.xml"), map[string]string{
			"CurrentMonthDownload": i64(d.st.monthDownload),
			"CurrentMonthUpload":   i64(d.st.monthUpload),
			"MonthDuration":        i64(d.st.monthDuration),
			"MonthLastClearTime":   d.st.monthLastClear,
		}), "", nil)

	case hilink.PathDialupDataSwitch:
		writeXML(w, rewriteElements(hilink.Fixture("dialup_mobile_dataswitch.xml"), map[string]string{
			"dataswitch": bit(d.st.dataOn),
		}), "", nil)

	case hilink.PathDialupConnection:
		writeXML(w, rewriteElements(hilink.Fixture("dialup_connection.xml"), map[string]string{
			"RoamAutoConnectEnable": d.st.roamAuto,
			"MaxIdelTime":           itoa(d.st.maxIdleTime),
			"ConnectMode":           d.st.connectMode,
			"MTU":                   d.st.mtu,
			"auto_dial_switch":      "1",
			"pdp_always_on":         d.st.pdpAlwaysOn,
		}), "", nil)

	case hilink.PathNetCurrentPLMN:
		writeXML(w, rewriteElements(hilink.Fixture("net_current_plmn.xml"), d.plmnValues()), "", nil)

	case hilink.PathNetRegister:
		writeXML(w, rewriteElements(hilink.Fixture("net_register.xml"), d.plmnValues()), "", nil)

	case hilink.PathNetNetMode:
		code, _ := hilink.NetworkModeCode(d.st.netMode)
		writeXML(w, rewriteElements(hilink.Fixture("net_net_mode.xml"), map[string]string{
			"NetworkMode": code,
			"NetworkBand": d.st.networkBand,
			"LTEBand":     d.st.lteBand,
		}), "", nil)

	case hilink.PathPinStatus:
		writeXML(w, rewriteElements(hilink.Fixture("pin_status.xml"), map[string]string{
			"SimState":    itoa(int(d.st.simState)),
			"PinOptState": itoa(d.st.pinOptState),
			"SimPinTimes": itoa(d.st.simPinTimes),
			"SimPukTimes": itoa(d.st.simPukTimes),
		}), "", nil)

	case hilink.PathDHCPSettings:
		writeXML(w, rewriteElements(hilink.Fixture("dhcp_settings.xml"), d.dhcpValues()), "", nil)

	case hilink.PathHiLinkLogin:
		writeXML(w, rewriteElements(hilink.Fixture("user_hilink_login.xml"), map[string]string{
			"hilink_login": bit(d.st.hilinkLogin),
		}), "", nil)

	default:
		writeAPIError(w, "", domain.CodeSystemNoSupport)
	}
}

func (d *SimDevice) plmnValues() map[string]string {
	return map[string]string{
		"State":     "0",
		"FullName":  d.st.carrier,
		"ShortName": d.st.carrier,
		"Numeric":   d.st.plmn,
		"Rat":       d.st.rat,
	}
}

func (d *SimDevice) dhcpValues() map[string]string {
	return map[string]string{
		"DnsStatus":          bit(d.st.dhcp.DNSStatus),
		"DhcpStartIPAddress": addrOrEmpty(d.st.dhcp.DHCPStartIPAddress),
		"DhcpIPAddress":      addrOrEmpty(d.st.dhcp.DHCPIPAddress),
		"DhcpStatus":         bit(d.st.dhcp.DHCPStatus),
		"DhcpLanNetmask":     addrOrEmpty(d.st.dhcp.DHCPLanNetmask),
		"SecondaryDns":       addrOrEmpty(d.st.dhcp.SecondaryDNS),
		"PrimaryDns":         addrOrEmpty(d.st.dhcp.PrimaryDNS),
		"DhcpEndIPAddress":   addrOrEmpty(d.st.dhcp.DHCPEndIPAddress),
		"DhcpLeaseTime":      itoa(d.st.dhcp.DHCPLeaseTime),
	}
}

func (d *SimDevice) post(w http.ResponseWriter, r *http.Request, path string, req reqValues) {
	switch path {
	case hilink.PathDialupDataSwitch:
		d.setDataSwitch(req.get("dataswitch") == "1")
		d.ok(w)

	case hilink.PathDialupConnection:
		d.mu.Lock()
		if v, ok := req.lookup("MaxIdelTime"); ok {
			d.st.maxIdleTime = atoi(v)
		}
		if v, ok := req.lookup("ConnectMode"); ok {
			d.st.connectMode = v
		}
		if v, ok := req.lookup("MTU"); ok {
			d.st.mtu = v
		}
		d.mu.Unlock()
		d.ok(w)

	case hilink.PathNetNetMode:
		m, ok := hilink.NetModeFromCode(req.get("NetworkMode"))
		if !ok {
			writeAPIError(w, "", domain.CodeFormatError)
			return
		}
		d.mu.Lock()
		if d.st.dataOn {
			d.mu.Unlock()
			writeAPIError(w, "", domain.CodeSetNetModeWhenDialup)
			return
		}
		d.st.netMode = m
		if v, ok := req.lookup("NetworkBand"); ok && v != "" {
			d.st.networkBand = v
		}
		if v, ok := req.lookup("LTEBand"); ok && v != "" {
			d.st.lteBand = v
		}
		d.mu.Unlock()
		d.ok(w)

	case hilink.PathNetRegister:
		d.ok(w)

	case hilink.PathDeviceControl:
		if req.get("Control") != itoa(hilink.ControlReboot) {
			writeAPIError(w, "", domain.CodeFormatError)
			return
		}
		d.reboot()
		d.ok(w)

	case hilink.PathDHCPSettings:
		d.setDHCP(w, r, req)

	case hilink.PathSMSList:
		d.smsList(w, req)

	case hilink.PathSMSSend:
		if len(req["Phone"]) == 0 {
			writeAPIError(w, "", domain.CodeFormatError)
			return
		}
		d.mu.Lock()
		for _, p := range req["Phone"] {
			d.st.messages = append(d.st.messages, Message{
				Index:    d.st.nextIndex,
				Phone:    p,
				Content:  req.get("Content"),
				Date:     d.now().Format(SMSDateLayout),
				Smstat:   3,
				SaveType: 3,
				Priority: 4,
				SmsType:  1,
				Box:      device.SMSBoxOutbox,
			})
			d.st.nextIndex++
		}
		d.mu.Unlock()
		d.ok(w)

	case hilink.PathSMSDelete:
		idx := atoi64(req.get("Index"))
		d.mu.Lock()
		kept := d.st.messages[:0]
		for _, m := range d.st.messages {
			if m.Index != idx {
				kept = append(kept, m)
			}
		}
		d.st.messages = kept
		d.mu.Unlock()
		d.ok(w)

	case hilink.PathSMSSetRead:
		idx := atoi64(req.get("Index"))
		d.mu.Lock()
		for i := range d.st.messages {
			if d.st.messages[i].Index == idx {
				d.st.messages[i].Smstat = 1
			}
		}
		d.mu.Unlock()
		d.ok(w)

	default:
		writeAPIError(w, "", domain.CodeSystemNoSupport)
	}
}

func (d *SimDevice) ok(w http.ResponseWriter) {
	writeXML(w, hilink.Fixture("response_ok.xml"), "", nil)
}

func (d *SimDevice) setDataSwitch(on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	if !on {
		if d.st.dataOn {
			d.st.totalConnectTime += d.st.connectTime(now)
		}
		d.st.dataOn = false
		d.st.dataOffAt = now
		d.st.connectedAt = time.Time{}
		return
	}
	if d.st.dataOn {
		d.st.lastActivity = now
		return
	}
	if !d.st.dataOffAt.IsZero() && now.Sub(d.st.dataOffAt) >= d.holdToNewIP {
		d.st.rotateIP()
	}
	d.st.dataOn = true
	d.st.connectedAt = now
	d.st.lastActivity = now
}

func (d *SimDevice) reboot() {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	d.st.dataOn = false
	d.st.dataOffAt = now
	d.st.connectedAt = time.Time{}
	d.st.currentUpload = 0
	d.st.currentDownload = 0
	d.tok.invalidateSession()
}

func (d *SimDevice) setDHCP(w http.ResponseWriter, r *http.Request, req reqValues) {
	newIP, err := netip.ParseAddr(req.get("DhcpIPAddress"))
	if err != nil {
		writeAPIError(w, "", domain.CodeFormatError)
		return
	}
	start, errStart := netip.ParseAddr(req.get("DhcpStartIPAddress"))
	end, errEnd := netip.ParseAddr(req.get("DhcpEndIPAddress"))
	if errStart != nil || errEnd != nil {
		writeAPIError(w, "", domain.CodeFormatError)
		return
	}
	if !sameSubnet24(newIP, start) || !sameSubnet24(newIP, end) {
		writeAPIError(w, "", domain.CodeFormatError)
		return
	}
	if _, ok := req.lookup("ShowDnsSetting"); !ok {
		writeAPIError(w, "", domain.CodeFormatError)
		return
	}

	d.mu.Lock()
	old := d.st.dhcp.DHCPIPAddress
	d.st.dhcp.DHCPIPAddress = newIP
	d.st.dhcp.DHCPStartIPAddress = start
	d.st.dhcp.DHCPEndIPAddress = end
	d.st.dhcp.DHCPStatus = req.get("DhcpStatus") == "1"
	d.st.dhcp.DNSStatus = req.get("DnsStatus") == "1"
	if v, err := netip.ParseAddr(req.get("DhcpLanNetmask")); err == nil {
		d.st.dhcp.DHCPLanNetmask = v
	}
	if v, err := netip.ParseAddr(req.get("PrimaryDns")); err == nil {
		d.st.dhcp.PrimaryDNS = v
	}
	if v, err := netip.ParseAddr(req.get("SecondaryDns")); err == nil {
		d.st.dhcp.SecondaryDNS = v
	}
	if v := atoi(req.get("DhcpLeaseTime")); v > 0 {
		d.st.dhcp.DHCPLeaseTime = v
	}
	moved := old != newIP
	d.dhcpMoved = d.dhcpMoved || moved
	onMove := d.onMove
	d.mu.Unlock()

	if onMove != nil && moved {
		onMove(old, newIP, d)
	}
	if moved {
		d.hang(r)
		return
	}
	d.ok(w)
}

func sameSubnet24(a, b netip.Addr) bool {
	if !a.Is4() || !b.Is4() {
		return false
	}
	x, y := a.As4(), b.As4()
	return x[0] == y[0] && x[1] == y[1] && x[2] == y[2]
}

func (d *SimDevice) smsList(w http.ResponseWriter, req reqValues) {
	box := device.SMSBox(atoi(req.get("BoxType")))
	if !box.Valid() {
		writeAPIError(w, "", domain.CodeFormatError)
		return
	}
	page := atoi(req.get("PageIndex"))
	if page <= 0 {
		page = 1
	}
	size := atoi(req.get("ReadCount"))
	if size <= 0 {
		size = 20
	}
	if size > maxSMSPageSize {
		size = maxSMSPageSize
	}
	if page > maxSMSPageIndex {
		page = maxSMSPageIndex
	}

	d.mu.Lock()
	all := make([]Message, 0, len(d.st.messages))
	for _, m := range d.st.messages {
		if m.Box == box {
			all = append(all, m)
		}
	}
	d.mu.Unlock()

	from := (page - 1) * size
	if from > len(all) {
		from = len(all)
	}
	to := from + size
	if to > len(all) {
		to = len(all)
	}
	pageItems := all[from:to]

	src := hilink.Fixture("sms_sms_list.xml")
	tmpl := firstMessageTemplate(src)
	var body bytes.Buffer
	for _, m := range pageItems {
		body.Write(rewriteElements(tmpl, map[string]string{
			"Smstat":   itoa(m.Smstat),
			"Index":    i64(m.Index),
			"Phone":    m.Phone,
			"Content":  m.Content,
			"Date":     m.Date,
			"Sca":      "",
			"SaveType": itoa(m.SaveType),
			"Priority": itoa(m.Priority),
			"SmsType":  itoa(m.SmsType),
		}))
	}
	if len(pageItems) == 0 {
		out := rewriteElements(hilink.Fixture("sms_sms_list_empty.xml"), map[string]string{"Count": itoa(len(all))})
		writeXML(w, out, "", nil)
		return
	}
	out := replaceBlock(src, "Messages", body.Bytes())
	out = rewriteElements(out, map[string]string{"Count": itoa(len(all))})
	writeXML(w, out, "", nil)
}

func firstMessageTemplate(src []byte) []byte {
	inner := innerBlock(src, "Messages")
	if inner == nil {
		return nil
	}
	open := []byte("<Message>")
	closing := []byte("</Message>")
	s := bytes.Index(inner, open)
	if s < 0 {
		return nil
	}
	e := bytes.Index(inner[s:], closing)
	if e < 0 {
		return nil
	}
	end := s + e + len(closing)
	out := make([]byte, 0, end-s+1)
	out = append(out, inner[s:end]...)
	out = append(out, '\n')
	return out
}

func (d *SimDevice) hang(r *http.Request) {
	d.pause(r, d.maxHang)
}

func (d *SimDevice) pause(r *http.Request, dur time.Duration) bool {
	t := time.NewTimer(dur)
	defer t.Stop()
	select {
	case <-r.Context().Done():
		return false
	case <-t.C:
		return true
	}
}

func sessionCookie(r *http.Request) string {
	raw := r.Header.Get(hilink.HeaderCookie)
	for _, part := range strings.Split(raw, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, hilink.CookieName+"=") {
			return part
		}
	}
	return ""
}

type reqValues map[string][]string

func (v reqValues) get(name string) string {
	if s := v[name]; len(s) > 0 {
		return s[0]
	}
	return ""
}

func (v reqValues) lookup(name string) (string, bool) {
	s, ok := v[name]
	if !ok || len(s) == 0 {
		return "", ok
	}
	return s[0], true
}

func parseRequest(body []byte) reqValues {
	out := reqValues{}
	dec := xml.NewDecoder(bytes.NewReader(body))
	var stack []string
	var text bytes.Buffer
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			stack = append(stack, t.Name.Local)
			text.Reset()
		case xml.CharData:
			text.Write(t)
		case xml.EndElement:
			if len(stack) == 0 {
				continue
			}
			name := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if name != "request" {
				out[name] = append(out[name], strings.TrimSpace(text.String()))
			}
			text.Reset()
		}
	}
	return out
}

func atoi(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

func atoi64(s string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func i64(n int64) string { return strconv.FormatInt(n, 10) }
