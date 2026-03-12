package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
)

type settingsRepo struct{ base }

func (r *settingsRepo) Get(ctx context.Context, key string) (string, error) {
	var v string
	err := r.q.QueryRowContext(ctx, `SELECT value FROM settings WHERE "key" = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", notFound("setting", key)
	}
	if err != nil {
		return "", mapErr(err, "get setting")
	}
	return v, nil
}

func (r *settingsRepo) Set(ctx context.Context, key, value string, nowMS int64) error {
	if key == "" {
		return errInvalid("setting key is required")
	}
	if nowMS == 0 {
		nowMS = r.now()
	}
	return r.exec(ctx, "set setting",
		`INSERT INTO settings("key", value, updated_at) VALUES(?,?,?)
		 ON CONFLICT("key") DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, nowMS)
}

func (r *settingsRepo) All(ctx context.Context) (map[string]string, error) {
	rows, err := r.q.QueryContext(ctx, `SELECT "key", value FROM settings ORDER BY "key"`)
	if err != nil {
		return nil, mapErr(err, "list settings")
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, mapErr(err, "scan setting")
		}
		out[k] = v
	}
	return out, mapErr(rows.Err(), "list settings")
}

func itoa(n int) string { return strconv.Itoa(n) }

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }
