package devops

import (
	"context"
	"errors"
	"strings"

	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/eventbus"
	"github.com/n4darae/huawei-API/src/internal/store"
)

const SMSPreviewRunes = 80

func (s *Service) SMSList(ctx context.Context, dongleID string, box device.SMSBox, page, size int) ([]device.SMS, int, error) {
	if !box.Valid() {
		return nil, 0, domain.Wrap(domain.ErrInvalid, "devops: sms box %d", int(box))
	}
	t, err := s.target(ctx, dongleID)
	if err != nil {
		return nil, 0, err
	}
	dev, devErr := s.deps.Dev.ForSlot(ctx, t.slot.Slot)
	if devErr == nil {
		msgs, total, err := dev.SMSList(ctx, box, page, size)
		if err == nil {
			s.persist(ctx, dongleID, msgs)
			return msgs, total, nil
		}
		devErr = err
	}
	cached, total, err := s.deps.Repos.SMS().List(ctx, store.SMSFilter{
		DongleID: dongleID,
		Box:      box,
		Offset:   offsetOf(page, size),
		Limit:    size,
	})
	if err != nil {
		return nil, 0, devErr
	}
	return cached, total, nil
}

func offsetOf(page, size int) int {
	if page < 1 || size < 1 {
		return 0
	}
	return (page - 1) * size
}

func (s *Service) persist(ctx context.Context, dongleID string, msgs []device.SMS) {
	if len(msgs) == 0 {
		return
	}
	known := map[int64]bool{}
	if rows, _, err := s.deps.Repos.SMS().List(ctx, store.SMSFilter{DongleID: dongleID, Box: msgs[0].Box}); err == nil {
		for _, r := range rows {
			known[r.Index] = true
		}
	}
	now := domain.UnixMillis(s.deps.Clock.Now())
	for _, m := range msgs {
		m.IsFragment = m.SmsType == device.SMSTypeFragment
		if err := s.deps.Repos.SMS().Upsert(ctx, dongleID, m, now); err != nil {
			continue
		}
		if m.Box == device.SMSBoxInbox && !known[m.Index] {
			s.publishSMS(ctx, dongleID, m)
		}
	}
}

func (s *Service) publishSMS(ctx context.Context, dongleID string, m device.SMS) {
	if s.deps.Bus == nil {
		return
	}
	ev, err := eventbus.NewEvent(s.deps.NodeID, eventbus.EvSMSReceived, dongleID, eventbus.SMSData{
		DongleID:   dongleID,
		Index:      m.Index,
		Phone:      m.Phone,
		Preview:    preview(m.Content),
		IsFragment: m.IsFragment,
		SentAt:     m.Date,
	})
	if err != nil {
		return
	}
	_ = s.deps.Bus.Publish(ctx, ev)
}

func preview(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= SMSPreviewRunes {
		return string(r)
	}
	return string(r[:SMSPreviewRunes])
}

func (s *Service) SMSSend(ctx context.Context, dongleID string, to []string, body string) error {
	t, err := s.target(ctx, dongleID)
	if err != nil {
		return err
	}
	dev, err := s.deps.Dev.ForSlot(ctx, t.slot.Slot)
	if err != nil {
		return err
	}
	return dev.SMSSend(ctx, to, body)
}

func (s *Service) SMSDelete(ctx context.Context, dongleID string, idx int64) error {
	t, err := s.target(ctx, dongleID)
	if err != nil {
		return err
	}
	dev, err := s.deps.Dev.ForSlot(ctx, t.slot.Slot)
	if err != nil {
		return err
	}
	if err := dev.SMSDelete(ctx, idx); err != nil {
		return err
	}
	return s.forEachBox(func(box device.SMSBox) error {
		return s.deps.Repos.SMS().Delete(ctx, dongleID, box, idx)
	})
}

func (s *Service) SMSMarkRead(ctx context.Context, dongleID string, idx int64) error {
	t, err := s.target(ctx, dongleID)
	if err != nil {
		return err
	}
	dev, err := s.deps.Dev.ForSlot(ctx, t.slot.Slot)
	if err != nil {
		return err
	}
	if err := dev.SMSSetRead(ctx, idx); err != nil {
		return err
	}
	return s.forEachBox(func(box device.SMSBox) error {
		return s.deps.Repos.SMS().MarkRead(ctx, dongleID, box, idx)
	})
}

func (s *Service) forEachBox(fn func(box device.SMSBox) error) error {
	var last error
	hit := false
	for _, box := range []device.SMSBox{device.SMSBoxInbox, device.SMSBoxOutbox, device.SMSBoxDraft} {
		err := fn(box)
		if err == nil {
			hit = true
			continue
		}
		if !errors.Is(err, domain.ErrNotFound) {
			last = err
		}
	}
	if hit {
		return nil
	}
	return last
}

func (s *Service) SyncSMS(ctx context.Context, dongleID string) (int, error) {
	t, err := s.target(ctx, dongleID)
	if err != nil {
		return 0, err
	}
	dev, err := s.deps.Dev.ForSlot(ctx, t.slot.Slot)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, box := range []device.SMSBox{device.SMSBoxInbox, device.SMSBoxOutbox} {
		msgs, _, err := dev.SMSList(ctx, box, 1, SMSSyncPageSize)
		if err != nil {
			return total, err
		}
		s.persist(ctx, dongleID, msgs)
		total += len(msgs)
	}
	return total, nil
}

const SMSSyncPageSize = 50
