package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var ErrSchemaAhead = errors.New("store: database schema is newer than this binary")

func (s *Store) Migrate(ctx context.Context) error {
	ms, err := Migrations()
	if err != nil {
		return err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	applied, err := appliedVersions(ctx, s.db)
	if err != nil {
		return err
	}
	if len(ms) > 0 && applied.max > ms[len(ms)-1].Version {
		return fmt.Errorf("%w: database is at %d, binary ships %d", ErrSchemaAhead, applied.max, ms[len(ms)-1].Version)
	}

	for _, m := range ms {
		if applied.have[m.Version] {
			continue
		}
		if err := s.applyMigration(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) applyMigration(ctx context.Context, m Migration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return mapErr(err, "begin migration")
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		return fmt.Errorf("store: migration %04d_%s: %w", m.Version, m.Name, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations(version, name, applied_at) VALUES(?,?,?)`,
		m.Version, m.Name, s.now()); err != nil {
		return fmt.Errorf("store: recording migration %04d_%s: %w", m.Version, m.Name, err)
	}
	if err := tx.Commit(); err != nil {
		return mapErr(err, "commit migration")
	}
	return nil
}

type versionSet struct {
	have map[int]bool
	max  int
}

func appliedVersions(ctx context.Context, q queryer) (versionSet, error) {
	out := versionSet{have: map[int]bool{}}

	var name string
	err := q.QueryRowContext(ctx,
		`SELECT name FROM sqlite_schema WHERE type='table' AND name='schema_migrations'`).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, mapErr(err, "look up schema_migrations")
	}

	rows, err := q.QueryContext(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return out, mapErr(err, "read schema_migrations")
	}
	defer rows.Close()
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return out, mapErr(err, "scan schema_migrations")
		}
		out.have[v] = true
		if v > out.max {
			out.max = v
		}
	}
	return out, mapErr(rows.Err(), "read schema_migrations")
}

func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	v, err := appliedVersions(ctx, s.db)
	if err != nil {
		return 0, err
	}
	return v.max, nil
}
