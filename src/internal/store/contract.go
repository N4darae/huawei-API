package store

import (
	"context"
	"net/netip"

	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/domain"
)

type DongleFilter struct {
	NodeID   string
	IMEI     string
	Carrier  string
	DongleID string
	Limit    int
}

type ProxyFilter struct {
	CustomerID       string
	SlotID           string
	Enabled          *bool
	Suspended        *bool
	ExpiringBeforeMS int64
	Limit            int
}

type OperationFilter struct {
	Kind        domain.OpKind
	State       domain.OpState
	Trigger     domain.Trigger
	SubjectType domain.SubjectType
	SubjectID   string
	SinceMS     int64
	Limit       int
}

type RotationFilter struct {
	ProxyID string
	SinceMS int64
	Limit   int
}

type SMSFilter struct {
	DongleID string
	Box      device.SMSBox
	Offset   int
	Limit    int
}

type NodeRepo interface {
	Get(ctx context.Context, id string) (domain.Node, error)
	List(ctx context.Context) ([]domain.Node, error)
	Upsert(ctx context.Context, n domain.Node) error
}

type DongleRepo interface {
	Get(ctx context.Context, id string) (domain.Dongle, error)
	GetByIMEI(ctx context.Context, imei string) (domain.Dongle, error)
	List(ctx context.Context, f DongleFilter) ([]domain.Dongle, error)
	Create(ctx context.Context, d domain.Dongle) error
	Update(ctx context.Context, d domain.Dongle) error
	Delete(ctx context.Context, id string) error
	SetAutoRecover(ctx context.Context, id string, enabled bool) error
	SetCapabilities(ctx context.Context, id string, lanIPChangeSupported, hilinkLoginRequired bool) error
	SetDataCap(ctx context.Context, id string, capBytes int64, resetDay int) error
}

type SlotRepo interface {
	Get(ctx context.Context, id string) (domain.SlotRow, error)
	GetBySlot(ctx context.Context, nodeID string, s domain.Slot) (domain.SlotRow, error)
	GetByDongle(ctx context.Context, dongleID string) (domain.SlotRow, error)
	List(ctx context.Context, nodeID string) ([]domain.SlotRow, error)
	Create(ctx context.Context, r domain.SlotRow) error
	Update(ctx context.Context, r domain.SlotRow) error
	Delete(ctx context.Context, id string) error
	Attach(ctx context.Context, slotID, dongleID string) error
	Detach(ctx context.Context, slotID string) error
	NextFree(ctx context.Context, nodeID string) (domain.Slot, error)
}

type ProxyRepo interface {
	Get(ctx context.Context, id string) (domain.Proxy, error)
	GetBySlot(ctx context.Context, slotID string) (domain.Proxy, error)
	GetByUsername(ctx context.Context, username string) (domain.Proxy, error)
	List(ctx context.Context, f ProxyFilter) ([]domain.Proxy, error)
	Create(ctx context.Context, p domain.Proxy) error
	Update(ctx context.Context, p domain.Proxy) error
	Delete(ctx context.Context, id string) error
	SetEnabled(ctx context.Context, id string, enabled bool) error
	SetSuspended(ctx context.Context, id string, suspended bool) error
	SetCredentials(ctx context.Context, id, username, password string) error
	SetAuthMode(ctx context.Context, id string, mode domain.AuthMode) error
	SetPolicy(ctx context.Context, id string, policy domain.ProxyPolicy) error
	SetCustomer(ctx context.Context, id string, customerID *string, expiresAt *int64) error
	ListAuthIPs(ctx context.Context, proxyID string) ([]domain.ProxyAuthIP, error)
	AddAuthIP(ctx context.Context, a domain.ProxyAuthIP) error
	DeleteAuthIP(ctx context.Context, proxyID string, cidr netip.Prefix) error
	ListExpired(ctx context.Context, nowMS int64) ([]domain.Proxy, error)
}

type OperationRepo interface {
	Get(ctx context.Context, id string) (domain.Operation, error)
	List(ctx context.Context, f OperationFilter) ([]domain.Operation, error)
	Create(ctx context.Context, o domain.Operation) error
	Progress(ctx context.Context, id string, state domain.OpState, step string, pct int) error
	Finish(ctx context.Context, id string, state domain.OpState, errMsg, resultJSON string, finishedAtMS int64) error
	FindActive(ctx context.Context, subjectType domain.SubjectType, subjectID string) (domain.Operation, error)
	ListActive(ctx context.Context) ([]domain.Operation, error)
	MarkStalled(ctx context.Context, nowMS int64) (int, error)
	ReconcileOrphans(ctx context.Context, nowMS int64) (int, error)
}

type RotationRepo interface {
	Create(ctx context.Context, r domain.Rotation) error
	List(ctx context.Context, f RotationFilter) ([]domain.Rotation, error)
	LastFor(ctx context.Context, proxyID string) (domain.Rotation, error)
}

type CustomerRepo interface {
	Get(ctx context.Context, id string) (domain.Customer, error)
	List(ctx context.Context) ([]domain.Customer, error)
	Create(ctx context.Context, c domain.Customer) error
	Update(ctx context.Context, c domain.Customer) error
	Delete(ctx context.Context, id string) error
	CountProxies(ctx context.Context, id string) (int, error)
}

type UsageRepo interface {
	AddDongleDaily(ctx context.Context, dongleID, day string, upBytes, downBytes int64, nowMS int64) error
	GetDongleDaily(ctx context.Context, dongleID, day string) (domain.UsageDay, error)
	ListDongleDaily(ctx context.Context, dongleID string, fromDay, toDay string) ([]domain.UsageDay, error)
	SumDongleSince(ctx context.Context, dongleID, fromDay string) (upBytes, downBytes int64, err error)
}

type SMSRepo interface {
	Upsert(ctx context.Context, dongleID string, m device.SMS, nowMS int64) error
	List(ctx context.Context, f SMSFilter) ([]device.SMS, int, error)
	Delete(ctx context.Context, dongleID string, box device.SMSBox, index int64) error
	MarkRead(ctx context.Context, dongleID string, box device.SMSBox, index int64) error
	CountUnread(ctx context.Context, dongleID string) (int, error)
}

type SettingsRepo interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string, nowMS int64) error
	All(ctx context.Context) (map[string]string, error)
}

type Repos interface {
	Nodes() NodeRepo
	Dongles() DongleRepo
	Slots() SlotRepo
	Proxies() ProxyRepo
	Operations() OperationRepo
	Rotations() RotationRepo
	Customers() CustomerRepo
	Usage() UsageRepo
	SMS() SMSRepo
	Settings() SettingsRepo
}

const (
	SettingLastBackupAt = "last_backup_at"
	SettingSchemaOwner  = "schema_owner"
	SettingHostBootID   = "host_boot_id"
)
