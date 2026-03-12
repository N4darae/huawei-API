package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

type dongleRepo struct{ base }

const dongleCols = `id, node_id, imei, iccid, imsi, firmware_ver, hw_ver, classify, carrier,
	lan_ip_change_supported, hilink_login_required, data_cap_bytes, cap_reset_day,
	auto_recover_enabled, created_at, updated_at`

func (r *dongleRepo) Get(ctx context.Context, id string) (domain.Dongle, error) {
	row := r.q.QueryRowContext(ctx, `SELECT `+dongleCols+` FROM dongles WHERE id = ?`, id)
	d, err := scanDongle(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Dongle{}, notFound("dongle", id)
	}
	if err != nil {
		return domain.Dongle{}, mapErr(err, "get dongle")
	}
	return d, nil
}

func (r *dongleRepo) GetByIMEI(ctx context.Context, imei string) (domain.Dongle, error) {
	row := r.q.QueryRowContext(ctx, `SELECT `+dongleCols+` FROM dongles WHERE imei = ?`, imei)
	d, err := scanDongle(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Dongle{}, notFound("dongle imei", imei)
	}
	if err != nil {
		return domain.Dongle{}, mapErr(err, "get dongle by imei")
	}
	return d, nil
}

func (r *dongleRepo) List(ctx context.Context, f DongleFilter) ([]domain.Dongle, error) {
	where := []string{}
	args := []any{}
	if f.NodeID != "" {
		where = append(where, "node_id = ?")
		args = append(args, f.NodeID)
	}
	if f.IMEI != "" {
		where = append(where, "imei = ?")
		args = append(args, f.IMEI)
	}
	if f.Carrier != "" {
		where = append(where, "carrier = ?")
		args = append(args, f.Carrier)
	}
	if f.DongleID != "" {
		where = append(where, "id = ?")
		args = append(args, f.DongleID)
	}

	q := `SELECT ` + dongleCols + ` FROM dongles`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY imei" + limitClause(f.Limit)

	rows, err := r.q.QueryContext(ctx, q, withLimit(args, f.Limit)...)
	if err != nil {
		return nil, mapErr(err, "list dongles")
	}
	defer rows.Close()

	out := []domain.Dongle{}
	for rows.Next() {
		d, err := scanDongle(rows)
		if err != nil {
			return nil, mapErr(err, "scan dongle")
		}
		out = append(out, d)
	}
	return out, mapErr(rows.Err(), "list dongles")
}

func (r *dongleRepo) Create(ctx context.Context, d domain.Dongle) error {
	if d.ID == "" || d.IMEI == "" {
		return errInvalid("dongle id and imei are required")
	}
	d = withDongleDefaults(d)
	created, updated := r.stamps(d.CreatedAt)
	return r.exec(ctx, "create dongle",
		`INSERT INTO dongles(`+dongleCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.ID, d.NodeID, d.IMEI, d.ICCID, d.IMSI, d.FirmwareVer, d.HwVer, d.Classify, d.Carrier,
		boolInt(d.LanIPChangeSupported), boolInt(d.HilinkLoginRequired), d.DataCapBytes, d.CapResetDay,
		boolInt(d.AutoRecoverEnabled), created, updated)
}

func (r *dongleRepo) Update(ctx context.Context, d domain.Dongle) error {
	if d.ID == "" {
		return errInvalid("dongle id is required")
	}
	d = withDongleDefaults(d)
	return r.execAffecting(ctx, "update dongle", d.ID,
		`UPDATE dongles SET node_id=?, imei=?, iccid=?, imsi=?, firmware_ver=?, hw_ver=?, classify=?,
		 carrier=?, lan_ip_change_supported=?, hilink_login_required=?, data_cap_bytes=?, cap_reset_day=?,
		 auto_recover_enabled=?, updated_at=? WHERE id=?`,
		d.NodeID, d.IMEI, d.ICCID, d.IMSI, d.FirmwareVer, d.HwVer, d.Classify, d.Carrier,
		boolInt(d.LanIPChangeSupported), boolInt(d.HilinkLoginRequired), d.DataCapBytes, d.CapResetDay,
		boolInt(d.AutoRecoverEnabled), r.now(), d.ID)
}

func (r *dongleRepo) Delete(ctx context.Context, id string) error {
	return r.execAffecting(ctx, "delete dongle", id, `DELETE FROM dongles WHERE id = ?`, id)
}

func (r *dongleRepo) SetAutoRecover(ctx context.Context, id string, enabled bool) error {
	return r.execAffecting(ctx, "set dongle auto_recover_enabled", id,
		`UPDATE dongles SET auto_recover_enabled=?, updated_at=? WHERE id=?`,
		boolInt(enabled), r.now(), id)
}

func (r *dongleRepo) SetCapabilities(ctx context.Context, id string, lanIPChangeSupported, hilinkLoginRequired bool) error {
	return r.execAffecting(ctx, "set dongle capabilities", id,
		`UPDATE dongles SET lan_ip_change_supported=?, hilink_login_required=?, updated_at=? WHERE id=?`,
		boolInt(lanIPChangeSupported), boolInt(hilinkLoginRequired), r.now(), id)
}

func (r *dongleRepo) SetDataCap(ctx context.Context, id string, capBytes int64, resetDay int) error {
	if resetDay < 1 || resetDay > 28 {
		return errInvalid("cap reset day %d is outside 1-28", resetDay)
	}
	return r.execAffecting(ctx, "set dongle data cap", id,
		`UPDATE dongles SET data_cap_bytes=?, cap_reset_day=?, updated_at=? WHERE id=?`,
		capBytes, resetDay, r.now(), id)
}

func withDongleDefaults(d domain.Dongle) domain.Dongle {
	if d.Classify == "" {
		d.Classify = domain.ClassifyHiLink
	}
	if d.CapResetDay == 0 {
		d.CapResetDay = 1
	}
	return d
}

func scanDongle(s scanner) (domain.Dongle, error) {
	var (
		d                          domain.Dongle
		lanIP, login, autoRecovery int
	)
	err := s.Scan(&d.ID, &d.NodeID, &d.IMEI, &d.ICCID, &d.IMSI, &d.FirmwareVer, &d.HwVer,
		&d.Classify, &d.Carrier, &lanIP, &login, &d.DataCapBytes, &d.CapResetDay,
		&autoRecovery, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return domain.Dongle{}, err
	}
	d.LanIPChangeSupported = lanIP == 1
	d.HilinkLoginRequired = login == 1
	d.AutoRecoverEnabled = autoRecovery == 1
	return d, nil
}
