package devops

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/device/sim"
	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/eventbus"
	"github.com/n4darae/huawei-API/src/internal/store"
)

func seedInbox(h *harness) (int64, int64) {
	d := h.farm.Device(1)
	short := d.AddMessage(sim.Message{
		Phone:   "+77010000001",
		Content: "balance is 1200",
		Smstat:  0,
		SmsType: 1,
		Box:     device.SMSBoxInbox,
	})
	long := d.AddMessage(sim.Message{
		Phone:   "+77010000002",
		Content: "part one of a long operator notice that the firmware chopped into pieces",
		Smstat:  0,
		SmsType: device.SMSTypeFragment,
		Box:     device.SMSBoxInbox,
	})
	return short, long
}

func TestSMSListFlagsFragmentsAndPersistsThem(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	short, long := seedInbox(h)

	msgs, total, err := h.svc.SMSList(context.Background(), dongleID(1), device.SMSBoxInbox, 1, 20)
	if err != nil {
		t.Fatalf("SMSList: %v", err)
	}
	if total != 2 || len(msgs) != 2 {
		t.Fatalf("SMSList returned %d of %d messages, want 2 of 2", len(msgs), total)
	}
	byIndex := map[int64]device.SMS{}
	for _, m := range msgs {
		byIndex[m.Index] = m
	}
	if byIndex[short].IsFragment {
		t.Fatalf("a single part message was flagged as a fragment: %+v", byIndex[short])
	}
	if !byIndex[long].IsFragment {
		t.Fatalf("SmsType %d was not flagged as a fragment: %+v", device.SMSTypeFragment, byIndex[long])
	}

	stored, storedTotal, err := h.db.SMS().List(context.Background(), store.SMSFilter{DongleID: dongleID(1), Box: device.SMSBoxInbox})
	if err != nil {
		t.Fatalf("stored SMS List: %v", err)
	}
	if storedTotal != 2 {
		t.Fatalf("inbox persisted %d rows, want 2", storedTotal)
	}
	for _, m := range stored {
		if m.Index == long && !m.IsFragment {
			t.Fatal("the fragment flag was lost on the way into sqlite")
		}
	}
}

func TestSMSListPublishesOnlyNewInboxMessages(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	seedInbox(h)

	if _, _, err := h.svc.SMSList(context.Background(), dongleID(1), device.SMSBoxInbox, 1, 20); err != nil {
		t.Fatalf("first SMSList: %v", err)
	}
	h.sink.waitFor(t, func([]eventbus.Event) bool { return len(smsEvents(h)) >= 2 })
	if got := smsEvents(h); len(got) != 2 {
		t.Fatalf("first sync published %d sms events, want 2", len(got))
	}
	if _, _, err := h.svc.SMSList(context.Background(), dongleID(1), device.SMSBoxInbox, 1, 20); err != nil {
		t.Fatalf("second SMSList: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if got := smsEvents(h); len(got) != 2 {
		t.Fatalf("re-reading the inbox published %d sms events, want the original 2", len(got))
	}
	for _, e := range smsEvents(h) {
		if e.DongleID != dongleID(1) || e.Preview == "" {
			t.Fatalf("sms event is %+v, want it to name the dongle and carry a preview", e)
		}
	}
}

func smsEvents(h *harness) []eventbus.SMSData {
	out := []eventbus.SMSData{}
	for _, e := range h.sink.Events() {
		if e.Type != eventbus.EvSMSReceived {
			continue
		}
		var d eventbus.SMSData
		if err := json.Unmarshal(e.Data, &d); err == nil {
			out = append(out, d)
		}
	}
	return out
}

func TestSMSListFallsBackToTheCacheWhenTheDongleIsSilent(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	seedInbox(h)

	if _, _, err := h.svc.SMSList(context.Background(), dongleID(1), device.SMSBoxInbox, 1, 20); err != nil {
		t.Fatalf("warm SMSList: %v", err)
	}
	h.farm.Device(1).SetUnreachable(true)

	msgs, total, err := h.svc.SMSList(context.Background(), dongleID(1), device.SMSBoxInbox, 1, 20)
	if err != nil {
		t.Fatalf("cached SMSList: %v", err)
	}
	if total != 2 || len(msgs) != 2 {
		t.Fatalf("cached SMSList returned %d of %d, want 2 of 2", len(msgs), total)
	}
}

func TestSMSSendAppearsInTheOutbox(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	if err := h.svc.SMSSend(context.Background(), dongleID(1), []string{"+77010000003"}, "hello"); err != nil {
		t.Fatalf("SMSSend: %v", err)
	}
	msgs, total, err := h.svc.SMSList(context.Background(), dongleID(1), device.SMSBoxOutbox, 1, 20)
	if err != nil {
		t.Fatalf("outbox SMSList: %v", err)
	}
	if total != 1 || len(msgs) != 1 || msgs[0].Content != "hello" {
		t.Fatalf("outbox holds %d messages %+v, want the sent one", total, msgs)
	}
}

func TestSMSSendWithoutARecipientIsRejected(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	err := h.svc.SMSSend(context.Background(), dongleID(1), nil, "hello")
	requireErrorIs(t, err, domain.ErrInvalid, "SMSSend without a recipient")
}

func TestSMSMarkReadAndDeleteReachBothTheDongleAndTheCache(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	short, _ := seedInbox(h)
	if _, _, err := h.svc.SMSList(context.Background(), dongleID(1), device.SMSBoxInbox, 1, 20); err != nil {
		t.Fatalf("warm SMSList: %v", err)
	}

	if err := h.svc.SMSMarkRead(context.Background(), dongleID(1), short); err != nil {
		t.Fatalf("SMSMarkRead: %v", err)
	}
	unread, err := h.db.SMS().CountUnread(context.Background(), dongleID(1))
	if err != nil {
		t.Fatalf("CountUnread: %v", err)
	}
	if unread != 1 {
		t.Fatalf("%d messages are still unread, want 1", unread)
	}

	if err := h.svc.SMSDelete(context.Background(), dongleID(1), short); err != nil {
		t.Fatalf("SMSDelete: %v", err)
	}
	_, total, err := h.db.SMS().List(context.Background(), store.SMSFilter{DongleID: dongleID(1)})
	if err != nil {
		t.Fatalf("List after delete: %v", err)
	}
	if total != 1 {
		t.Fatalf("%d rows survive the delete, want 1", total)
	}
	for _, m := range h.farm.Device(1).Messages() {
		if m.Index == short {
			t.Fatal("the message was deleted from the cache but not from the dongle")
		}
	}
}

func TestSyncSMSWalksInboxAndOutbox(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	seedInbox(h)
	if err := h.svc.SMSSend(context.Background(), dongleID(1), []string{"+77010000004"}, "out"); err != nil {
		t.Fatalf("SMSSend: %v", err)
	}

	n, err := h.svc.SyncSMS(context.Background(), dongleID(1))
	if err != nil {
		t.Fatalf("SyncSMS: %v", err)
	}
	if n != 3 {
		t.Fatalf("SyncSMS stored %d messages, want 3", n)
	}
	_, total, err := h.db.SMS().List(context.Background(), store.SMSFilter{DongleID: dongleID(1)})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 3 {
		t.Fatalf("sqlite holds %d messages, want 3", total)
	}
}
