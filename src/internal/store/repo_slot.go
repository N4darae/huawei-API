package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

type slotRepo struct{ base }

const slotCols = `id, node_id, slot, usb_path, id_path, if_name, dongle_id, created_at, updated_at`

func (r *slotRepo) Get(ctx context.Context, id string) (domain.SlotRow, error) {
	row := r.q.QueryRowContext(ctx, `SELECT `+slotCols+` FROM slots WHERE id = ?`, id)
	s, err := scanSlot(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SlotRow{}, notFound("slot", id)
	}
	if err != nil {
		return domain.SlotRow{}, mapErr(err, "get slot")
	}
	return s, nil
}

func (r *slotRepo) GetBySlot(ctx context.Context, nodeID string, s domain.Slot) (domain.SlotRow, error) {
	row := r.q.QueryRowContext(ctx,
		`SELECT `+slotCols+` FROM slots WHERE node_id = ? AND slot = ?`, nodeID, int(s))
	out, err := scanSlot(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SlotRow{}, notFound("slot", fmt.Sprintf("%s/%d", nodeID, int(s)))
	}
	if err != nil {
		return domain.SlotRow{}, mapErr(err, "get slot by number")
	}
	return out, nil
}

func (r *slotRepo) GetByDongle(ctx context.Context, dongleID string) (domain.SlotRow, error) {
	row := r.q.QueryRowContext(ctx, `SELECT `+slotCols+` FROM slots WHERE dongle_id = ?`, dongleID)
	out, err := scanSlot(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SlotRow{}, notFound("slot for dongle", dongleID)
	}
	if err != nil {
		return domain.SlotRow{}, mapErr(err, "get slot by dongle")
	}
	return out, nil
}

func (r *slotRepo) List(ctx context.Context, nodeID string) ([]domain.SlotRow, error) {
	q := `SELECT ` + slotCols + ` FROM slots`
	args := []any{}
	if nodeID != "" {
		q += " WHERE node_id = ?"
		args = append(args, nodeID)
	}
	q += " ORDER BY node_id, slot"

	rows, err := r.q.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, mapErr(err, "list slots")
	}
	defer rows.Close()

	out := []domain.SlotRow{}
	for rows.Next() {
		s, err := scanSlot(rows)
		if err != nil {
			return nil, mapErr(err, "scan slot")
		}
		out = append(out, s)
	}
	return out, mapErr(rows.Err(), "list slots")
}

func (r *slotRepo) Create(ctx context.Context, s domain.SlotRow) error {
	if s.ID == "" {
		return errInvalid("slot id is required")
	}
	if !s.Slot.Valid() {
		return errInvalid("slot %d is outside 1-%d", int(s.Slot), domain.MaxSlots)
	}
	if s.IfName == "" {
		s.IfName = s.Slot.IfaceName()
	}
	created, updated := r.stamps(s.CreatedAt)
	return r.exec(ctx, "create slot",
		`INSERT INTO slots(`+slotCols+`) VALUES(?,?,?,?,?,?,?,?,?)`,
		s.ID, s.NodeID, int(s.Slot), s.USBPath, s.IDPath, s.IfName, nullText(s.DongleID), created, updated)
}

func (r *slotRepo) Update(ctx context.Context, s domain.SlotRow) error {
	if s.ID == "" {
		return errInvalid("slot id is required")
	}
	if !s.Slot.Valid() {
		return errInvalid("slot %d is outside 1-%d", int(s.Slot), domain.MaxSlots)
	}
	if s.IfName == "" {
		s.IfName = s.Slot.IfaceName()
	}
	return r.execAffecting(ctx, "update slot", s.ID,
		`UPDATE slots SET node_id=?, slot=?, usb_path=?, id_path=?, if_name=?, dongle_id=?, updated_at=? WHERE id=?`,
		s.NodeID, int(s.Slot), s.USBPath, s.IDPath, s.IfName, nullText(s.DongleID), r.now(), s.ID)
}

func (r *slotRepo) Delete(ctx context.Context, id string) error {
	return r.execAffecting(ctx, "delete slot", id, `DELETE FROM slots WHERE id = ?`, id)
}

func (r *slotRepo) Attach(ctx context.Context, slotID, dongleID string) error {
	if dongleID == "" {
		return errInvalid("dongle id is required to attach")
	}
	cur, err := r.Get(ctx, slotID)
	if err != nil {
		return err
	}
	if cur.Occupied() && *cur.DongleID != dongleID {
		return fmt.Errorf("slot %q already holds dongle %q: %w", slotID, *cur.DongleID, domain.ErrSlotOccupied)
	}
	other, err := r.GetByDongle(ctx, dongleID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	if err == nil && other.ID != slotID {
		return fmt.Errorf("dongle %q is already attached to slot %q: %w", dongleID, other.ID, domain.ErrConflict)
	}
	return r.execAffecting(ctx, "attach dongle to slot", slotID,
		`UPDATE slots SET dongle_id=?, updated_at=? WHERE id=?`, dongleID, r.now(), slotID)
}

func (r *slotRepo) Detach(ctx context.Context, slotID string) error {
	return r.execAffecting(ctx, "detach dongle from slot", slotID,
		`UPDATE slots SET dongle_id=NULL, updated_at=? WHERE id=?`, r.now(), slotID)
}

func (r *slotRepo) NextFree(ctx context.Context, nodeID string) (domain.Slot, error) {
	rows, err := r.q.QueryContext(ctx, `SELECT slot FROM slots WHERE node_id = ? ORDER BY slot`, nodeID)
	if err != nil {
		return 0, mapErr(err, "find next free slot")
	}
	defer rows.Close()

	taken := make(map[int]bool, domain.MaxSlots)
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			return 0, mapErr(err, "scan slot number")
		}
		taken[n] = true
	}
	if err := rows.Err(); err != nil {
		return 0, mapErr(err, "find next free slot")
	}
	for i := 1; i <= domain.MaxSlots; i++ {
		if !taken[i] {
			return domain.Slot(i), nil
		}
	}
	return 0, fmt.Errorf("node %q holds all %d slots: %w", nodeID, domain.MaxSlots, domain.ErrNoFreeSlot)
}

func scanSlot(s scanner) (domain.SlotRow, error) {
	var (
		out    domain.SlotRow
		number int
		dongle sql.NullString
	)
	err := s.Scan(&out.ID, &out.NodeID, &number, &out.USBPath, &out.IDPath, &out.IfName,
		&dongle, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		return domain.SlotRow{}, err
	}
	out.Slot = domain.Slot(number)
	out.DongleID = textPtr(dongle)
	return out, nil
}
