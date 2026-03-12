package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

type usageRepo struct{ base }

const usageCols = `dongle_id, day, up_bytes, down_bytes, updated_at`

const DayLayout = "2006-01-02"

func (r *usageRepo) AddDongleDaily(ctx context.Context, dongleID, day string, upBytes, downBytes int64, nowMS int64) error {
	if dongleID == "" || day == "" {
		return errInvalid("usage dongle_id and day are required")
	}
	if upBytes < 0 || downBytes < 0 {
		return errInvalid("usage deltas must not be negative, got up=%d down=%d", upBytes, downBytes)
	}
	if nowMS == 0 {
		nowMS = r.now()
	}
	return r.exec(ctx, "add dongle daily usage",
		`INSERT INTO usage_daily(`+usageCols+`) VALUES(?,?,?,?,?)
		 ON CONFLICT(dongle_id, day) DO UPDATE SET
		   up_bytes = usage_daily.up_bytes + excluded.up_bytes,
		   down_bytes = usage_daily.down_bytes + excluded.down_bytes,
		   updated_at = excluded.updated_at`,
		dongleID, day, upBytes, downBytes, nowMS)
}

func (r *usageRepo) GetDongleDaily(ctx context.Context, dongleID, day string) (domain.UsageDay, error) {
	row := r.q.QueryRowContext(ctx,
		`SELECT `+usageCols+` FROM usage_daily WHERE dongle_id = ? AND day = ?`, dongleID, day)
	u, err := scanUsage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.UsageDay{}, notFound("usage for", dongleID+" "+day)
	}
	if err != nil {
		return domain.UsageDay{}, mapErr(err, "get dongle daily usage")
	}
	return u, nil
}

func (r *usageRepo) ListDongleDaily(ctx context.Context, dongleID string, fromDay, toDay string) ([]domain.UsageDay, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+usageCols+` FROM usage_daily
		 WHERE dongle_id = ? AND day >= ? AND day <= ? ORDER BY day`,
		dongleID, fromDay, toDay)
	if err != nil {
		return nil, mapErr(err, "list dongle daily usage")
	}
	defer rows.Close()

	out := []domain.UsageDay{}
	for rows.Next() {
		u, err := scanUsage(rows)
		if err != nil {
			return nil, mapErr(err, "scan dongle daily usage")
		}
		out = append(out, u)
	}
	return out, mapErr(rows.Err(), "list dongle daily usage")
}

func (r *usageRepo) SumDongleSince(ctx context.Context, dongleID, fromDay string) (int64, int64, error) {
	var up, down sql.NullInt64
	err := r.q.QueryRowContext(ctx,
		`SELECT sum(up_bytes), sum(down_bytes) FROM usage_daily WHERE dongle_id = ? AND day >= ?`,
		dongleID, fromDay).Scan(&up, &down)
	if err != nil {
		return 0, 0, mapErr(err, "sum dongle usage")
	}
	return up.Int64, down.Int64, nil
}

func scanUsage(s scanner) (domain.UsageDay, error) {
	var u domain.UsageDay
	if err := s.Scan(&u.DongleID, &u.Day, &u.UpBytes, &u.DownBytes, &u.UpdatedAt); err != nil {
		return domain.UsageDay{}, err
	}
	return u, nil
}
