package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

type rotationRepo struct{ base }

const rotationCols = `id, operation_id, proxy_id, requested_at, duration_ms, old_public_ip,
	new_public_ip, ip_changed, result, trigger_kind, request_id, hold_ms, attempts, error`

func (r *rotationRepo) Create(ctx context.Context, rot domain.Rotation) error {
	if rot.ID == "" || rot.OperationID == "" || rot.ProxyID == "" {
		return errInvalid("rotation id, operation_id and proxy_id are required")
	}
	if !rot.Trigger.Valid() {
		return errInvalid("rotation trigger %q is not admin_ui, customer_api or auto_recovery", string(rot.Trigger))
	}
	switch rot.Result {
	case domain.RotationChanged, domain.RotationUnchanged, domain.RotationFailed:
	default:
		return errInvalid("rotation result %q is not changed, unchanged or failed", string(rot.Result))
	}
	if rot.RequestedAt == 0 {
		rot.RequestedAt = r.now()
	}
	return r.exec(ctx, "create rotation",
		`INSERT INTO rotations(`+rotationCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		rot.ID, rot.OperationID, rot.ProxyID, rot.RequestedAt, rot.DurationMS,
		addrText(rot.OldPublicIP), addrText(rot.NewPublicIP), boolInt(rot.IPChanged),
		string(rot.Result), string(rot.Trigger), rot.RequestID, rot.HoldMS, rot.Attempts, rot.Error)
}

func (r *rotationRepo) List(ctx context.Context, f RotationFilter) ([]domain.Rotation, error) {
	where := []string{}
	args := []any{}
	if f.ProxyID != "" {
		where = append(where, "proxy_id = ?")
		args = append(args, f.ProxyID)
	}
	if f.SinceMS > 0 {
		where = append(where, "requested_at >= ?")
		args = append(args, f.SinceMS)
	}

	q := `SELECT ` + rotationCols + ` FROM rotations`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY requested_at DESC, id DESC" + limitClause(f.Limit)

	rows, err := r.q.QueryContext(ctx, q, withLimit(args, f.Limit)...)
	if err != nil {
		return nil, mapErr(err, "list rotations")
	}
	defer rows.Close()

	out := []domain.Rotation{}
	for rows.Next() {
		rot, err := scanRotation(rows)
		if err != nil {
			return nil, mapErr(err, "scan rotation")
		}
		out = append(out, rot)
	}
	return out, mapErr(rows.Err(), "list rotations")
}

func (r *rotationRepo) LastFor(ctx context.Context, proxyID string) (domain.Rotation, error) {
	row := r.q.QueryRowContext(ctx,
		`SELECT `+rotationCols+` FROM rotations WHERE proxy_id = ? ORDER BY requested_at DESC, id DESC LIMIT 1`,
		proxyID)
	rot, err := scanRotation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Rotation{}, notFound("rotation for proxy", proxyID)
	}
	if err != nil {
		return domain.Rotation{}, mapErr(err, "get last rotation")
	}
	return rot, nil
}

func scanRotation(s scanner) (domain.Rotation, error) {
	var (
		rot            domain.Rotation
		oldIP, newIP   string
		changed        int
		result, trigge string
	)
	err := s.Scan(&rot.ID, &rot.OperationID, &rot.ProxyID, &rot.RequestedAt, &rot.DurationMS,
		&oldIP, &newIP, &changed, &result, &trigge, &rot.RequestID, &rot.HoldMS, &rot.Attempts, &rot.Error)
	if err != nil {
		return domain.Rotation{}, err
	}
	a, err := parseAddr("rotation old_public_ip", oldIP)
	if err != nil {
		return domain.Rotation{}, err
	}
	b, err := parseAddr("rotation new_public_ip", newIP)
	if err != nil {
		return domain.Rotation{}, err
	}
	rot.OldPublicIP = a
	rot.NewPublicIP = b
	rot.IPChanged = changed == 1
	rot.Result = domain.RotationResult(result)
	rot.Trigger = domain.Trigger(trigge)
	return rot, nil
}
