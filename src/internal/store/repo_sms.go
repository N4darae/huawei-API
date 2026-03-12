package store

import (
	"context"
	"strings"

	"github.com/n4darae/huawei-API/src/internal/device"
)

type smsRepo struct{ base }

const smsCols = `id, dongle_id, idx, box, phone, content, sent_at, is_read, sms_type, is_fragment, created_at`

const SMSListMaxLimit = 500

func (r *smsRepo) Upsert(ctx context.Context, dongleID string, m device.SMS, nowMS int64) error {
	if dongleID == "" {
		return errInvalid("sms dongle_id is required")
	}
	if !m.Box.Valid() {
		return errInvalid("sms box %d is not inbox, outbox or draft", int(m.Box))
	}
	if nowMS == 0 {
		nowMS = r.now()
	}
	fragment := m.IsFragment || m.SmsType == device.SMSTypeFragment
	return r.exec(ctx, "upsert sms",
		`INSERT INTO sms(`+smsCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(dongle_id, box, idx) DO UPDATE SET
		   phone = excluded.phone,
		   content = excluded.content,
		   sent_at = excluded.sent_at,
		   is_read = excluded.is_read,
		   sms_type = excluded.sms_type,
		   is_fragment = excluded.is_fragment`,
		smsRowID(dongleID, m.Box, m.Index), dongleID, m.Index, int(m.Box), m.Phone, m.Content,
		m.Date, boolInt(m.Read), m.SmsType, boolInt(fragment), nowMS)
}

func (r *smsRepo) List(ctx context.Context, f SMSFilter) ([]device.SMS, int, error) {
	where := []string{}
	args := []any{}
	if f.DongleID != "" {
		where = append(where, "dongle_id = ?")
		args = append(args, f.DongleID)
	}
	if f.Box != 0 {
		where = append(where, "box = ?")
		args = append(args, int(f.Box))
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}

	total, err := r.count(ctx, "count sms", `SELECT count(*) FROM sms`+clause, args...)
	if err != nil {
		return nil, 0, err
	}

	limit := f.Limit
	if limit <= 0 || limit > SMSListMaxLimit {
		limit = SMSListMaxLimit
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}

	rows, err := r.q.QueryContext(ctx,
		`SELECT `+smsCols+` FROM sms`+clause+` ORDER BY sent_at DESC, idx DESC LIMIT ? OFFSET ?`,
		append(append([]any{}, args...), limit, offset)...)
	if err != nil {
		return nil, 0, mapErr(err, "list sms")
	}
	defer rows.Close()

	out := []device.SMS{}
	for rows.Next() {
		m, err := scanSMS(rows)
		if err != nil {
			return nil, 0, mapErr(err, "scan sms")
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, mapErr(err, "list sms")
	}
	return out, total, nil
}

func (r *smsRepo) Delete(ctx context.Context, dongleID string, box device.SMSBox, index int64) error {
	return r.execAffecting(ctx, "delete sms", smsRowID(dongleID, box, index),
		`DELETE FROM sms WHERE dongle_id = ? AND box = ? AND idx = ?`, dongleID, int(box), index)
}

func (r *smsRepo) MarkRead(ctx context.Context, dongleID string, box device.SMSBox, index int64) error {
	return r.execAffecting(ctx, "mark sms read", smsRowID(dongleID, box, index),
		`UPDATE sms SET is_read = 1 WHERE dongle_id = ? AND box = ? AND idx = ?`, dongleID, int(box), index)
}

func (r *smsRepo) CountUnread(ctx context.Context, dongleID string) (int, error) {
	return r.count(ctx, "count unread sms",
		`SELECT count(*) FROM sms WHERE dongle_id = ? AND box = ? AND is_read = 0`,
		dongleID, int(device.SMSBoxInbox))
}

func smsRowID(dongleID string, box device.SMSBox, index int64) string {
	return dongleID + ":" + strings.TrimSpace(itoa(int(box))) + ":" + itoa64(index)
}

func scanSMS(s scanner) (device.SMS, error) {
	var (
		m        device.SMS
		id       string
		dongleID string
		box      int
		read     int
		fragment int
		created  int64
	)
	err := s.Scan(&id, &dongleID, &m.Index, &box, &m.Phone, &m.Content, &m.Date,
		&read, &m.SmsType, &fragment, &created)
	if err != nil {
		return device.SMS{}, err
	}
	m.Box = device.SMSBox(box)
	m.Read = read == 1
	m.IsFragment = fragment == 1
	return m, nil
}
