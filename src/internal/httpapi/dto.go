package httpapi

import (
	"encoding/json"
	"net/netip"

	"github.com/n4darae/huawei-API/src/internal/auth"
	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/proxysup"
	"github.com/n4darae/huawei-API/src/internal/reconcile"
)

type PortRange struct {
	Lo int `json:"lo"`
	Hi int `json:"hi"`
}

type ProxyPolicy struct {
	AllowAllPorts bool        `json:"allow_all_ports"`
	AllowedPorts  []PortRange `json:"allowed_ports"`
	MaxConn       int         `json:"max_conn"`
	ConnLimit     int         `json:"conn_limit"`
}

type PortsBound struct {
	Socks   bool `json:"socks"`
	HTTP    bool `json:"http"`
	ProbeOK bool `json:"probe_ok"`
}

type Proxy struct {
	ID                string      `json:"id"`
	Slot              int         `json:"slot"`
	State             string      `json:"state"`
	Host              string      `json:"host"`
	SocksPort         int         `json:"socks_port"`
	HTTPPort          int         `json:"http_port"`
	Username          string      `json:"username"`
	Password          string      `json:"password,omitempty"`
	AuthMode          string      `json:"auth_mode"`
	AuthIPCount       int         `json:"auth_ip_count"`
	Enabled           bool        `json:"enabled"`
	Suspended         bool        `json:"suspended"`
	CustomerID        *string     `json:"customer_id"`
	CustomerName      string      `json:"customer_name,omitempty"`
	ExpiresAt         *int64      `json:"expires_at"`
	WanIP             string      `json:"wan_ip,omitempty"`
	SignalBars        int         `json:"signal_bars,omitempty"`
	DataUsedBytes     int64       `json:"data_used_bytes,omitempty"`
	DataCapBytes      int64       `json:"data_cap_bytes,omitempty"`
	PortsBound        PortsBound  `json:"ports_bound"`
	Policy            ProxyPolicy `json:"policy"`
	ActiveOperationID *string     `json:"active_operation_id"`
	UpdatedAt         int64       `json:"updated_at"`
}

type ProxyList struct {
	Items []Proxy `json:"items"`
	Total int     `json:"total"`
}

type ProxyDetail struct {
	Proxy        Proxy     `json:"proxy"`
	AuthIPs      []AuthIP  `json:"auth_ips"`
	Slot         *Slot     `json:"slot,omitempty"`
	LastRotation *Rotation `json:"last_rotation,omitempty"`
}

type AuthIP struct {
	ID        string `json:"id"`
	CIDR      string `json:"cidr"`
	Note      string `json:"note,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

type AuthIPList struct {
	Items []AuthIP `json:"items"`
}

type Slot struct {
	ID         string  `json:"id"`
	Slot       int     `json:"slot"`
	IfName     string  `json:"if_name"`
	USBPath    string  `json:"usb_path"`
	IDPath     string  `json:"id_path,omitempty"`
	Occupied   bool    `json:"occupied"`
	DongleID   *string `json:"dongle_id"`
	HostIP     string  `json:"host_ip,omitempty"`
	GatewayIP  string  `json:"gateway_ip,omitempty"`
	RouteTable int     `json:"route_table,omitempty"`
}

type SlotList struct {
	Items []Slot `json:"items"`
}

type Dongle struct {
	ID                   string  `json:"id"`
	IMEI                 string  `json:"imei"`
	ICCID                string  `json:"iccid,omitempty"`
	IMSI                 string  `json:"imsi,omitempty"`
	FirmwareVer          string  `json:"firmware_ver,omitempty"`
	HwVer                string  `json:"hw_ver,omitempty"`
	Carrier              string  `json:"carrier,omitempty"`
	Slot                 int     `json:"slot"`
	ConnStatus           int     `json:"conn_status"`
	SimState             int     `json:"sim_state,omitempty"`
	NetMode              string  `json:"net_mode,omitempty"`
	WanIP                string  `json:"wan_ip,omitempty"`
	LanIPChangeSupported bool    `json:"lan_ip_change_supported"`
	HilinkLoginRequired  bool    `json:"hilink_login_required"`
	AutoRecoverEnabled   bool    `json:"auto_recover_enabled"`
	DataCapBytes         int64   `json:"data_cap_bytes,omitempty"`
	CapResetDay          int     `json:"cap_reset_day,omitempty"`
	Reachable            bool    `json:"reachable"`
	ObservedAt           int64   `json:"observed_at,omitempty"`
	ActiveOperationID    *string `json:"active_operation_id"`
}

type DongleList struct {
	Items []Dongle `json:"items"`
}

type Signal struct {
	RSSI   int    `json:"rssi,omitempty"`
	RSRP   int    `json:"rsrp,omitempty"`
	RSRQ   int    `json:"rsrq,omitempty"`
	SINR   int    `json:"sinr,omitempty"`
	Bars   int    `json:"bars"`
	Band   string `json:"band,omitempty"`
	CellID string `json:"cell_id,omitempty"`
	PLMN   string `json:"plmn,omitempty"`
	Mode   string `json:"mode,omitempty"`
}

type Traffic struct {
	CurrentUpload       int64 `json:"current_upload,omitempty"`
	CurrentDownload     int64 `json:"current_download,omitempty"`
	CurrentUploadRate   int64 `json:"current_upload_rate,omitempty"`
	CurrentDownloadRate int64 `json:"current_download_rate,omitempty"`
	TotalUpload         int64 `json:"total_upload,omitempty"`
	TotalDownload       int64 `json:"total_download,omitempty"`
	CurrentConnectTime  int64 `json:"current_connect_time,omitempty"`
	MonthUpload         int64 `json:"month_upload,omitempty"`
	MonthDownload       int64 `json:"month_download,omitempty"`
}

type DongleDetail struct {
	Dongle    Dongle   `json:"dongle"`
	Signal    *Signal  `json:"signal,omitempty"`
	Traffic   *Traffic `json:"traffic,omitempty"`
	Slot      *Slot    `json:"slot,omitempty"`
	UnreadSMS int      `json:"unread_sms"`
}

type Sms struct {
	Index      int64  `json:"index"`
	Phone      string `json:"phone"`
	Content    string `json:"content"`
	SentAt     int64  `json:"sent_at"`
	Box        int    `json:"box"`
	Read       bool   `json:"read"`
	SmsType    int    `json:"sms_type,omitempty"`
	IsFragment bool   `json:"is_fragment"`
}

type SmsList struct {
	Items []Sms `json:"items"`
	Total int   `json:"total"`
}

type Operation struct {
	ID          string         `json:"id"`
	Kind        string         `json:"kind"`
	SubjectType string         `json:"subject_type"`
	SubjectID   string         `json:"subject_id"`
	State       string         `json:"state"`
	Step        string         `json:"step"`
	Pct         int            `json:"pct"`
	StartedAt   int64          `json:"started_at"`
	DeadlineAt  int64          `json:"deadline_at"`
	FinishedAt  *int64         `json:"finished_at"`
	Error       string         `json:"error,omitempty"`
	Result      map[string]any `json:"result,omitempty"`
	Trigger     string         `json:"trigger"`
	ActorType   string         `json:"actor_type,omitempty"`
	RequestID   string         `json:"request_id,omitempty"`
}

type OperationList struct {
	Items []Operation `json:"items"`
}

type OperationAccepted struct {
	OperationID string `json:"operation_id"`
	PollURL     string `json:"poll_url"`
	State       string `json:"state,omitempty"`
	DeadlineAt  int64  `json:"deadline_at,omitempty"`
}

type Rotation struct {
	ID          string `json:"id"`
	RequestedAt int64  `json:"requested_at"`
	DurationMS  int    `json:"duration_ms"`
	OldPublicIP string `json:"old_public_ip,omitempty"`
	NewPublicIP string `json:"new_public_ip,omitempty"`
	IPChanged   bool   `json:"ip_changed"`
	Result      string `json:"result"`
	RequestID   string `json:"request_id,omitempty"`
}

type RotationList struct {
	Items []Rotation `json:"items"`
}

type RotateResult struct {
	OperationID string `json:"operation_id"`
	Result      string `json:"result"`
	IPChanged   bool   `json:"ip_changed"`
	OldIP       string `json:"old_ip,omitempty"`
	NewIP       string `json:"new_ip,omitempty"`
	DurationMS  int    `json:"duration_ms,omitempty"`
	Error       string `json:"error,omitempty"`
}

type CustomerStatus struct {
	ProxyID            string `json:"proxy_id"`
	State              string `json:"state"`
	Host               string `json:"host"`
	SocksPort          int    `json:"socks_port"`
	HTTPPort           int    `json:"http_port"`
	WanIP              string `json:"wan_ip,omitempty"`
	ExpiresAt          *int64 `json:"expires_at"`
	LastRotatedAt      int64  `json:"last_rotated_at,omitempty"`
	MinRotateIntervalS int    `json:"min_rotate_interval_s"`
	RotateAvailableAt  int64  `json:"rotate_available_at,omitempty"`
}

type SelftestResult struct {
	SocksOK   bool   `json:"socks_ok"`
	HTTPOK    bool   `json:"http_ok"`
	EgressOK  bool   `json:"egress_ok"`
	EgressIP  string `json:"egress_ip,omitempty"`
	LatencyMS int    `json:"latency_ms,omitempty"`
	Error     string `json:"error,omitempty"`
}

type Customer struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Contact    string `json:"contact,omitempty"`
	Note       string `json:"note,omitempty"`
	ProxyCount int    `json:"proxy_count"`
	CreatedAt  int64  `json:"created_at"`
}

type CustomerList struct {
	Items []Customer `json:"items"`
}

type ApiKey struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Prefix     string      `json:"prefix"`
	CustomerID *string     `json:"customer_id"`
	Scopes     []string    `json:"scopes"`
	ProxyIDs   []string    `json:"proxy_ids"`
	LastUsedAt *int64      `json:"last_used_at"`
	RevokedAt  *int64      `json:"revoked_at"`
	CreatedAt  int64       `json:"created_at"`
	LinkTokens []LinkToken `json:"link_tokens"`
}

type ApiKeyList struct {
	Items []ApiKey `json:"items"`
}

type ApiKeyCreated struct {
	Key    ApiKey `json:"key"`
	Secret string `json:"secret"`
}

type LinkToken struct {
	ID        string `json:"id"`
	APIKeyID  string `json:"api_key_id"`
	RevokedAt *int64 `json:"revoked_at"`
	CreatedAt int64  `json:"created_at"`
}

type LinkTokenCreated struct {
	Token LinkToken `json:"token"`
	URL   string    `json:"url"`
}

type SessionBody struct {
	Username  string `json:"username"`
	ExpiresAt int64  `json:"expires_at"`
	CSRFToken string `json:"csrf_token"`
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type SetAuthRequest struct {
	AuthMode       string  `json:"auth_mode"`
	Username       *string `json:"username"`
	Password       *string `json:"password"`
	RotatePassword bool    `json:"rotate_password"`
}

type AuthIPRequest struct {
	CIDR string `json:"cidr"`
	Note string `json:"note"`
}

type EnableRequest struct {
	Enabled bool `json:"enabled"`
}

type AssignCustomerRequest struct {
	CustomerID *string `json:"customer_id"`
	ExpiresAt  *int64  `json:"expires_at"`
}

type DonglePatchRequest struct {
	AutoRecoverEnabled *bool   `json:"auto_recover_enabled"`
	DataCapBytes       *int64  `json:"data_cap_bytes"`
	CapResetDay        *int    `json:"cap_reset_day"`
	Carrier            *string `json:"carrier"`
}

type NetModeRequest struct {
	NetMode string `json:"net_mode"`
}

type LanIPRequest struct {
	Gateway string `json:"gateway"`
}

type SmsSendRequest struct {
	To   []string `json:"to"`
	Body string   `json:"body"`
}

type SmsIndexRequest struct {
	Index int64 `json:"index"`
}

type CustomerRequest struct {
	Name    string `json:"name"`
	Contact string `json:"contact"`
	Note    string `json:"note"`
}

type ApiKeyRequest struct {
	Name       string   `json:"name"`
	CustomerID *string  `json:"customer_id"`
	Scopes     []string `json:"scopes"`
	ProxyIDs   []string `json:"proxy_ids"`
}

func addrText(a netip.Addr) string {
	if !a.IsValid() || a.IsUnspecified() {
		return ""
	}
	return a.String()
}

func policyDTO(p domain.ProxyPolicy) ProxyPolicy {
	out := ProxyPolicy{
		AllowAllPorts: p.AllowAllPorts,
		AllowedPorts:  []PortRange{},
		MaxConn:       p.MaxConn,
		ConnLimit:     p.ConnLimit,
	}
	for _, r := range p.AllowedPorts {
		out.AllowedPorts = append(out.AllowedPorts, PortRange{Lo: r.Lo, Hi: r.Hi})
	}
	return out
}

func policyFrom(in ProxyPolicy) domain.ProxyPolicy {
	out := domain.ProxyPolicy{
		AllowAllPorts: in.AllowAllPorts,
		MaxConn:       in.MaxConn,
		ConnLimit:     in.ConnLimit,
	}
	for _, r := range in.AllowedPorts {
		out.AllowedPorts = append(out.AllowedPorts, domain.PortRange{Lo: r.Lo, Hi: r.Hi})
	}
	return out
}

func slotDTO(row domain.SlotRow) Slot {
	return Slot{
		ID:         row.ID,
		Slot:       row.Slot.Int(),
		IfName:     row.IfName,
		USBPath:    row.USBPath,
		IDPath:     row.IDPath,
		Occupied:   row.Occupied(),
		DongleID:   row.DongleID,
		HostIP:     addrText(row.Slot.HostIP()),
		GatewayIP:  addrText(row.Slot.GatewayIP()),
		RouteTable: row.Slot.RouteTable(),
	}
}

func authIPDTO(a domain.ProxyAuthIP) AuthIP {
	return AuthIP{ID: a.ID, CIDR: a.CIDR.String(), Note: a.Note, CreatedAt: a.CreatedAt}
}

func dongleDTO(d domain.Dongle, row domain.SlotRow, obs reconcile.DeviceObservation, activeOpID string) Dongle {
	out := Dongle{
		ID:                   d.ID,
		IMEI:                 d.IMEI,
		ICCID:                d.ICCID,
		IMSI:                 d.IMSI,
		FirmwareVer:          d.FirmwareVer,
		HwVer:                d.HwVer,
		Carrier:              d.Carrier,
		Slot:                 row.Slot.Int(),
		ConnStatus:           int(obs.Conn),
		SimState:             int(obs.Sim),
		NetMode:              string(obs.NetMode),
		WanIP:                addrText(obs.WanIP),
		LanIPChangeSupported: d.LanIPChangeSupported,
		HilinkLoginRequired:  d.HilinkLoginRequired,
		AutoRecoverEnabled:   d.AutoRecoverEnabled,
		DataCapBytes:         d.DataCapBytes,
		CapResetDay:          d.CapResetDay,
		Reachable:            obs.Reachable,
	}
	if !obs.ObservedAt.IsZero() {
		out.ObservedAt = domain.UnixMillis(obs.ObservedAt)
	}
	if activeOpID != "" {
		out.ActiveOperationID = &activeOpID
	}
	if out.ConnStatus == 0 {
		out.ConnStatus = int(device.ConnDisconnected)
	}
	return out
}

func signalDTO(s device.Signal) *Signal {
	if s == (device.Signal{}) {
		return nil
	}
	return &Signal{
		RSSI: s.RSSI, RSRP: s.RSRP, RSRQ: s.RSRQ, SINR: s.SINR,
		Bars: s.Bars, Band: s.Band, CellID: s.CellID, PLMN: s.PLMN, Mode: s.Mode,
	}
}

func trafficDTO(t device.Traffic, monthUp, monthDown int64) *Traffic {
	if t == (device.Traffic{}) && monthUp == 0 && monthDown == 0 {
		return nil
	}
	return &Traffic{
		CurrentUpload:       t.CurrentUpload,
		CurrentDownload:     t.CurrentDownload,
		CurrentUploadRate:   t.CurrentUploadRate,
		CurrentDownloadRate: t.CurrentDownloadRate,
		TotalUpload:         t.TotalUpload,
		TotalDownload:       t.TotalDownload,
		CurrentConnectTime:  t.CurrentConnectTime,
		MonthUpload:         monthUp,
		MonthDownload:       monthDown,
	}
}

func smsDTO(m device.SMS) Sms {
	return Sms{
		Index:      m.Index,
		Phone:      m.Phone,
		Content:    m.Content,
		SentAt:     m.Date,
		Box:        int(m.Box),
		Read:       m.Read,
		SmsType:    m.SmsType,
		IsFragment: m.IsFragment,
	}
}

func operationDTO(o domain.Operation) Operation {
	out := Operation{
		ID:          o.ID,
		Kind:        string(o.Kind),
		SubjectType: string(o.SubjectType),
		SubjectID:   o.SubjectID,
		State:       string(o.State),
		Step:        o.Step,
		Pct:         o.Pct,
		StartedAt:   o.StartedAt,
		DeadlineAt:  o.DeadlineAt,
		FinishedAt:  o.FinishedAt,
		Error:       o.Error,
		Trigger:     string(o.Trigger),
		ActorType:   string(o.ActorType),
		RequestID:   o.RequestID,
	}
	out.Result = decodeResult(o.ResultJSON)
	return out
}

func decodeResult(raw string) map[string]any {
	if raw == "" {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	if len(out) == 0 {
		return nil
	}
	if old, ok := out["old_public_ip"]; ok {
		if _, dup := out["old_ip"]; !dup {
			out["old_ip"] = old
		}
	}
	if fresh, ok := out["new_public_ip"]; ok {
		if _, dup := out["new_ip"]; !dup {
			out["new_ip"] = fresh
		}
	}
	return out
}

func acceptedDTO(o domain.Operation) OperationAccepted {
	return OperationAccepted{
		OperationID: o.ID,
		PollURL:     operationURL(o.ID),
		State:       string(o.State),
		DeadlineAt:  o.DeadlineAt,
	}
}

func rotationDTO(r domain.Rotation) Rotation {
	return Rotation{
		ID:          r.ID,
		RequestedAt: r.RequestedAt,
		DurationMS:  r.DurationMS,
		OldPublicIP: addrText(r.OldPublicIP),
		NewPublicIP: addrText(r.NewPublicIP),
		IPChanged:   r.IPChanged,
		Result:      string(r.Result),
		RequestID:   r.RequestID,
	}
}

func customerDTO(c domain.Customer, proxyCount int) Customer {
	return Customer{
		ID:         c.ID,
		Name:       c.Name,
		Contact:    c.Contact,
		Note:       c.Note,
		ProxyCount: proxyCount,
		CreatedAt:  c.CreatedAt,
	}
}

func keyDTO(k auth.Key) ApiKey {
	out := ApiKey{
		ID:         k.ID,
		Name:       k.Name,
		Prefix:     k.Prefix,
		CustomerID: k.CustomerID,
		Scopes:     k.Scopes,
		ProxyIDs:   k.ProxyIDs,
		LastUsedAt: k.LastUsedAt,
		RevokedAt:  k.RevokedAt,
		CreatedAt:  k.CreatedAt,
		LinkTokens: []LinkToken{},
	}
	if out.Scopes == nil {
		out.Scopes = []string{}
	}
	if out.ProxyIDs == nil {
		out.ProxyIDs = []string{}
	}
	for _, t := range k.LinkTokens {
		out.LinkTokens = append(out.LinkTokens, linkTokenDTO(t))
	}
	return out
}

func linkTokenDTO(t auth.LinkToken) LinkToken {
	return LinkToken{ID: t.ID, APIKeyID: t.APIKeyID, RevokedAt: t.RevokedAt, CreatedAt: t.CreatedAt}
}

func portsBoundDTO(st proxysup.Status) PortsBound {
	return PortsBound{Socks: st.SocksBound, HTTP: st.HTTPBound, ProbeOK: st.ProbeOK}
}
