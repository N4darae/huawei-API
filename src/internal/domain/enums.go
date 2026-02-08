package domain

type OpKind string

const (
	OpRotate     OpKind = "rotate"
	OpReboot     OpKind = "reboot"
	OpSetAuth    OpKind = "set_auth"
	OpSetPorts   OpKind = "set_ports"
	OpSetLanIP   OpKind = "set_lan_ip"
	OpSetNetMode OpKind = "set_net_mode"
	OpEnroll     OpKind = "enroll"
	OpSelftest   OpKind = "selftest"
)

func AllOpKinds() []OpKind {
	return []OpKind{OpRotate, OpReboot, OpSetAuth, OpSetPorts, OpSetLanIP, OpSetNetMode, OpEnroll, OpSelftest}
}

func (k OpKind) Valid() bool {
	for _, v := range AllOpKinds() {
		if v == k {
			return true
		}
	}
	return false
}

type OpState string

const (
	OpPending   OpState = "pending"
	OpRunning   OpState = "running"
	OpStalled   OpState = "stalled"
	OpSucceeded OpState = "succeeded"
	OpFailed    OpState = "failed"
	OpCanceled  OpState = "canceled"
)

func AllOpStates() []OpState {
	return []OpState{OpPending, OpRunning, OpStalled, OpSucceeded, OpFailed, OpCanceled}
}

func (s OpState) Terminal() bool {
	return s == OpSucceeded || s == OpFailed || s == OpCanceled
}

func (s OpState) Valid() bool {
	for _, v := range AllOpStates() {
		if v == s {
			return true
		}
	}
	return false
}

type Trigger string

const (
	TriggerAdminUI      Trigger = "admin_ui"
	TriggerCustomerAPI  Trigger = "customer_api"
	TriggerAutoRecovery Trigger = "auto_recovery"
)

func AllTriggers() []Trigger {
	return []Trigger{TriggerAdminUI, TriggerCustomerAPI, TriggerAutoRecovery}
}

func (t Trigger) Valid() bool {
	for _, v := range AllTriggers() {
		if v == t {
			return true
		}
	}
	return false
}

type SubjectType string

const (
	SubjectProxy  SubjectType = "proxy"
	SubjectDongle SubjectType = "dongle"
	SubjectSlot   SubjectType = "slot"
	SubjectNode   SubjectType = "node"
)

func AllSubjectTypes() []SubjectType {
	return []SubjectType{SubjectProxy, SubjectDongle, SubjectSlot, SubjectNode}
}

type ActorType string

const (
	ActorAdmin  ActorType = "admin"
	ActorAPIKey ActorType = "api_key"
	ActorSystem ActorType = "system"
)

type AuthMode string

const (
	AuthUserPass AuthMode = "userpass"
	AuthIPList   AuthMode = "iplist"
	AuthBoth     AuthMode = "both"
)

func AllAuthModes() []AuthMode {
	return []AuthMode{AuthUserPass, AuthIPList, AuthBoth}
}

func (m AuthMode) Valid() bool {
	for _, v := range AllAuthModes() {
		if v == m {
			return true
		}
	}
	return false
}

func (m AuthMode) UsesUserPass() bool { return m == AuthUserPass || m == AuthBoth }

func (m AuthMode) UsesIPList() bool { return m == AuthIPList || m == AuthBoth }

type RotationResult string

const (
	RotationChanged   RotationResult = "changed"
	RotationUnchanged RotationResult = "unchanged"
	RotationFailed    RotationResult = "failed"
)

func AllRotationResults() []RotationResult {
	return []RotationResult{RotationChanged, RotationUnchanged, RotationFailed}
}

type ProxyState string

const (
	ProxyStateActive    ProxyState = "active"
	ProxyStateSuspended ProxyState = "suspended"
	ProxyStateDisabled  ProxyState = "disabled"
	ProxyStateExpired   ProxyState = "expired"
	ProxyStateDegraded  ProxyState = "degraded"
	ProxyStateUnknown   ProxyState = "unknown"
)

type RotateStep string

const (
	StepPrecheck    RotateStep = "precheck"
	StepFence       RotateStep = "fence"
	StepDataOff     RotateStep = "data_off"
	StepHold        RotateStep = "hold"
	StepDataOn      RotateStep = "data_on"
	StepWaitConnect RotateStep = "wait_connect"
	StepUnfence     RotateStep = "unfence"
	StepVerify      RotateStep = "verify"
	StepDone        RotateStep = "done"
)

func RotateSteps() []RotateStep {
	return []RotateStep{StepPrecheck, StepFence, StepDataOff, StepHold, StepDataOn, StepWaitConnect, StepUnfence, StepVerify, StepDone}
}

const (
	InvariantPublicSrcRule    = "public_src_rule_present"
	InvariantNoForeignRule    = "no_foreign_rule_below_900"
	InvariantCustomerLeg      = "customer_leg_ok"
	InvariantEgressFenced     = "egress_fenced"
	InvariantDNSContained     = "dns_contained"
	InvariantNftCustomerHits  = "nft_customer_accept_hits"
	InvariantNftTablePresent  = "nft_table_present"
	InvariantRpFilterAll      = "rp_filter_all"
	InvariantIPForward        = "ip_forward"
	InvariantNoDuplicateAddrs = "no_duplicate_addrs"
)

func AllInvariants() []string {
	return []string{
		InvariantPublicSrcRule,
		InvariantNoForeignRule,
		InvariantCustomerLeg,
		InvariantEgressFenced,
		InvariantDNSContained,
		InvariantNftCustomerHits,
		InvariantNftTablePresent,
		InvariantRpFilterAll,
		InvariantIPForward,
		InvariantNoDuplicateAddrs,
	}
}
