package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

type operationRepo struct{ base }

const operationCols = `id, kind, subject_type, subject_id, state, step, pct, started_at, deadline_at,
	finished_at, error, result_json, trigger_kind, actor_type, actor_id, request_id, created_at, updated_at`

const OrphanedByRestart = "orphaned by a panel restart; no process was adopted"

const StalledPastDeadline = "stalled; the operation passed its deadline without finishing"

func (r *operationRepo) Get(ctx context.Context, id string) (domain.Operation, error) {
	row := r.q.QueryRowContext(ctx, `SELECT `+operationCols+` FROM operations WHERE id = ?`, id)
	o, err := scanOperation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Operation{}, notFound("operation", id)
	}
	if err != nil {
		return domain.Operation{}, mapErr(err, "get operation")
	}
	return o, nil
}

func (r *operationRepo) List(ctx context.Context, f OperationFilter) ([]domain.Operation, error) {
	where := []string{}
	args := []any{}
	if f.Kind != "" {
		where = append(where, "kind = ?")
		args = append(args, string(f.Kind))
	}
	if f.State != "" {
		where = append(where, "state = ?")
		args = append(args, string(f.State))
	}
	if f.Trigger != "" {
		where = append(where, "trigger_kind = ?")
		args = append(args, string(f.Trigger))
	}
	if f.SubjectType != "" {
		where = append(where, "subject_type = ?")
		args = append(args, string(f.SubjectType))
	}
	if f.SubjectID != "" {
		where = append(where, "subject_id = ?")
		args = append(args, f.SubjectID)
	}
	if f.SinceMS > 0 {
		where = append(where, "started_at >= ?")
		args = append(args, f.SinceMS)
	}

	q := `SELECT ` + operationCols + ` FROM operations`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY started_at DESC, id DESC" + limitClause(f.Limit)

	return r.many(ctx, "list operations", q, withLimit(args, f.Limit)...)
}

func (r *operationRepo) Create(ctx context.Context, o domain.Operation) error {
	if o.ID == "" || o.SubjectID == "" {
		return errInvalid("operation id and subject_id are required")
	}
	if !o.Kind.Valid() {
		return errInvalid("operation kind %q is unknown", string(o.Kind))
	}
	if !o.Trigger.Valid() {
		return errInvalid("operation trigger %q is not admin_ui, customer_api or auto_recovery", string(o.Trigger))
	}
	if o.State == "" {
		o.State = domain.OpPending
	}
	if !o.State.Valid() {
		return errInvalid("operation state %q is unknown", string(o.State))
	}
	created, updated := r.stamps(o.CreatedAt)
	if o.StartedAt == 0 {
		o.StartedAt = created
	}

	err := r.exec(ctx, "create operation",
		`INSERT INTO operations(`+operationCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		o.ID, string(o.Kind), string(o.SubjectType), o.SubjectID, string(o.State), o.Step, o.Pct,
		o.StartedAt, o.DeadlineAt, nullInt(o.FinishedAt), o.Error, o.ResultJSON,
		string(o.Trigger), string(o.ActorType), o.ActorID, o.RequestID, created, updated)
	if errors.Is(err, domain.ErrConflict) && o.FinishedAt == nil {
		if live, lookupErr := r.FindActive(ctx, o.SubjectType, o.SubjectID); lookupErr == nil && live.ID != o.ID {
			return fmt.Errorf("operation %q is already live on %s: %w", live.ID, live.Target(), domain.ErrOpInProgress)
		}
	}
	return err
}

func (r *operationRepo) Progress(ctx context.Context, id string, state domain.OpState, step string, pct int) error {
	if !state.Valid() {
		return errInvalid("operation state %q is unknown", string(state))
	}
	if state.Terminal() {
		return errInvalid("Progress cannot move operation %q to the terminal state %q; use Finish", id, string(state))
	}
	if pct < 0 || pct > 100 {
		return errInvalid("operation pct %d is outside 0-100", pct)
	}
	return r.execAffecting(ctx, "progress operation", id,
		`UPDATE operations SET state=?, step=?, pct=?, updated_at=? WHERE id=? AND finished_at IS NULL`,
		string(state), step, pct, r.now(), id)
}

func (r *operationRepo) Finish(ctx context.Context, id string, state domain.OpState, errMsg, resultJSON string, finishedAtMS int64) error {
	if !state.Terminal() {
		return errInvalid("Finish requires a terminal state, got %q", string(state))
	}
	if finishedAtMS == 0 {
		finishedAtMS = r.now()
	}
	return r.execAffecting(ctx, "finish operation", id,
		`UPDATE operations SET state=?, error=?, result_json=?, finished_at=?, pct=100, updated_at=?
		 WHERE id=? AND finished_at IS NULL`,
		string(state), errMsg, resultJSON, finishedAtMS, r.now(), id)
}

func (r *operationRepo) FindActive(ctx context.Context, subjectType domain.SubjectType, subjectID string) (domain.Operation, error) {
	row := r.q.QueryRowContext(ctx,
		`SELECT `+operationCols+` FROM operations
		 WHERE subject_type = ? AND subject_id = ? AND finished_at IS NULL`,
		string(subjectType), subjectID)
	o, err := scanOperation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Operation{}, notFound("active operation on", string(subjectType)+":"+subjectID)
	}
	if err != nil {
		return domain.Operation{}, mapErr(err, "find active operation")
	}
	return o, nil
}

func (r *operationRepo) ListActive(ctx context.Context) ([]domain.Operation, error) {
	return r.many(ctx, "list active operations",
		`SELECT `+operationCols+` FROM operations WHERE finished_at IS NULL ORDER BY started_at`)
}

func (r *operationRepo) MarkStalled(ctx context.Context, nowMS int64) (int, error) {
	res, err := r.q.ExecContext(ctx,
		`UPDATE operations SET state=?, error=?, finished_at=?, updated_at=?
		 WHERE finished_at IS NULL AND deadline_at > 0 AND deadline_at < ? AND state IN (?,?)`,
		string(domain.OpStalled), StalledPastDeadline, nowMS, nowMS, nowMS, string(domain.OpPending), string(domain.OpRunning))
	if err != nil {
		return 0, mapErr(err, "mark stalled operations")
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, mapErr(err, "mark stalled operations")
	}
	return int(n), nil
}

func (r *operationRepo) ReconcileOrphans(ctx context.Context, nowMS int64) (int, error) {
	res, err := r.q.ExecContext(ctx,
		`UPDATE operations SET state=?, error=?, finished_at=?, updated_at=?
		 WHERE finished_at IS NULL`,
		string(domain.OpFailed), OrphanedByRestart, nowMS, nowMS)
	if err != nil {
		return 0, mapErr(err, "reconcile orphaned operations")
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, mapErr(err, "reconcile orphaned operations")
	}
	return int(n), nil
}

func (r *operationRepo) many(ctx context.Context, what, query string, args ...any) ([]domain.Operation, error) {
	rows, err := r.q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, mapErr(err, what)
	}
	defer rows.Close()

	out := []domain.Operation{}
	for rows.Next() {
		o, err := scanOperation(rows)
		if err != nil {
			return nil, mapErr(err, what)
		}
		out = append(out, o)
	}
	return out, mapErr(rows.Err(), what)
}

func scanOperation(s scanner) (domain.Operation, error) {
	var (
		o                                 domain.Operation
		kind, subjectType, state, trigger string
		actorType                         string
		finished                          sql.NullInt64
	)
	err := s.Scan(&o.ID, &kind, &subjectType, &o.SubjectID, &state, &o.Step, &o.Pct,
		&o.StartedAt, &o.DeadlineAt, &finished, &o.Error, &o.ResultJSON,
		&trigger, &actorType, &o.ActorID, &o.RequestID, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		return domain.Operation{}, err
	}
	o.Kind = domain.OpKind(kind)
	o.SubjectType = domain.SubjectType(subjectType)
	o.State = domain.OpState(state)
	o.Trigger = domain.Trigger(trigger)
	o.ActorType = domain.ActorType(actorType)
	o.FinishedAt = intPtr(finished)
	return o, nil
}
