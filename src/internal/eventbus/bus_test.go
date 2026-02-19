package eventbus

import (
	"context"
	"errors"
	"testing"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

func TestMemBusDeliversByTopic(t *testing.T) {
	ctx := context.Background()
	b := NewMemBus(4)
	defer b.Close()

	proxies, cancelProxies, err := b.Subscribe(ctx, []string{TopicProxies})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer cancelProxies()

	all, cancelAll, err := b.Subscribe(ctx, nil)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer cancelAll()

	ev, err := NewEvent("local", EvDonglePatch, "d1", PatchData{ID: "d1", Fields: map[string]any{"reachable": true}})
	if err != nil {
		t.Fatalf("new event: %v", err)
	}
	if err := b.Publish(ctx, ev); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case got := <-all:
		if got.Type != EvDonglePatch || got.Topic != TopicDongles {
			t.Fatalf("wildcard subscriber got %+v", got)
		}
	default:
		t.Fatal("wildcard subscriber received nothing")
	}
	select {
	case got := <-proxies:
		t.Fatalf("proxies subscriber received a dongles event: %+v", got)
	default:
	}
}

func TestMemBusRejectsUnknownEventType(t *testing.T) {
	b := NewMemBus(1)
	defer b.Close()
	err := b.Publish(context.Background(), Event{Type: "proxy.deleted", Topic: TopicProxies})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("Publish accepted an unknown event type: %v", err)
	}
}

func TestMemBusDropsLaggingSubscriber(t *testing.T) {
	ctx := context.Background()
	b := NewMemBus(1)
	defer b.Close()

	ch, cancel, err := b.Subscribe(ctx, nil)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer cancel()

	ev, err := NewEvent("local", EvSystemNotice, "", NoticeData{Level: NoticeWarn, Title: "x"})
	if err != nil {
		t.Fatalf("new event: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := b.Publish(ctx, ev); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}
	if b.Lagged() == 0 {
		t.Fatal("a subscriber that never reads must be recorded as lagged")
	}
	<-ch
	if _, open := <-ch; open {
		t.Fatal("a lagging subscriber must have its channel closed so the client refetches")
	}
	if b.Subscribers() != 0 {
		t.Fatalf("lagging subscriber still registered: %d", b.Subscribers())
	}
}

func TestMemBusCancelClosesChannel(t *testing.T) {
	b := NewMemBus(2)
	defer b.Close()
	ch, cancel, err := b.Subscribe(context.Background(), []string{TopicOperations})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	cancel()
	cancel()
	if _, open := <-ch; open {
		t.Fatal("cancel must close the subscriber channel")
	}
}

func TestEventTypeTopicsAreKnown(t *testing.T) {
	known := map[string]bool{}
	for _, tp := range AllTopics() {
		known[tp] = true
	}
	for _, e := range AllEventTypes() {
		if !known[e.Topic()] {
			t.Errorf("event %q maps to unknown topic %q", e, e.Topic())
		}
		if !e.Valid() {
			t.Errorf("event %q fails its own Valid check", e)
		}
	}
	if EventType("proxy.deleted").Valid() {
		t.Error("Valid accepted an unknown event type")
	}
}
