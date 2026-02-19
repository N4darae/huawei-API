package eventbus

import (
	"context"
	"encoding/json"
	"time"
)

type EventType string

const (
	EvHello        EventType = "hello"
	EvProxyPatch   EventType = "proxy.patch"
	EvDonglePatch  EventType = "dongle.patch"
	EvOpUpdate     EventType = "op.update"
	EvOpDone       EventType = "op.done"
	EvSMSReceived  EventType = "sms.received"
	EvSystemNotice EventType = "system.notice"
)

func AllEventTypes() []EventType {
	return []EventType{
		EvHello,
		EvProxyPatch,
		EvDonglePatch,
		EvOpUpdate,
		EvOpDone,
		EvSMSReceived,
		EvSystemNotice,
	}
}

func (t EventType) Valid() bool {
	for _, v := range AllEventTypes() {
		if v == t {
			return true
		}
	}
	return false
}

const (
	TopicProxies    = "proxies"
	TopicDongles    = "dongles"
	TopicOperations = "operations"
	TopicSMS        = "sms"
	TopicSystem     = "system"
	TopicAll        = "*"
)

func AllTopics() []string {
	return []string{TopicProxies, TopicDongles, TopicOperations, TopicSMS, TopicSystem}
}

func (t EventType) Topic() string {
	switch t {
	case EvProxyPatch:
		return TopicProxies
	case EvDonglePatch:
		return TopicDongles
	case EvOpUpdate, EvOpDone:
		return TopicOperations
	case EvSMSReceived:
		return TopicSMS
	default:
		return TopicSystem
	}
}

type Event struct {
	Type    EventType       `json:"type"`
	Topic   string          `json:"topic"`
	NodeID  string          `json:"node_id"`
	Subject string          `json:"subject"`
	TS      int64           `json:"ts"`
	Data    json.RawMessage `json:"data"`
}

type Bus interface {
	Publish(ctx context.Context, e Event) error
	Subscribe(ctx context.Context, topics []string) (<-chan Event, func(), error)
}

type HelloData struct {
	NodeID     string   `json:"node_id"`
	ServerTime int64    `json:"server_time"`
	Topics     []string `json:"topics"`
	Product    string   `json:"product"`
}

type PatchData struct {
	ID     string         `json:"id"`
	Fields map[string]any `json:"fields"`
}

type SMSData struct {
	DongleID   string `json:"dongle_id"`
	Index      int64  `json:"index"`
	Phone      string `json:"phone"`
	Preview    string `json:"preview"`
	IsFragment bool   `json:"is_fragment"`
	SentAt     int64  `json:"sent_at"`
}

type NoticeData struct {
	Level  string `json:"level"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

const (
	NoticeInfo  = "info"
	NoticeWarn  = "warn"
	NoticeError = "error"
)

func NewEvent(nodeID string, t EventType, subject string, data any) (Event, error) {
	raw := json.RawMessage("null")
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			return Event{}, err
		}
		raw = b
	}
	return Event{
		Type:    t,
		Topic:   t.Topic(),
		NodeID:  nodeID,
		Subject: subject,
		TS:      time.Now().UnixMilli(),
		Data:    raw,
	}, nil
}
