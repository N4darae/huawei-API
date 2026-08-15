package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/secrets"

	sqlite "modernc.org/sqlite"
)

const (
	TxLock       = "immediate"
	MaxOpenConns = 4
	DirMode      = 0o750
)

var (
	ErrSealerMissing = errors.New("store: a secrets.Sealer is required; passwords are encrypted at rest")
	ErrPathMissing   = errors.New("store: database path is required")
	ErrPragma        = errors.New("store: connection pragma did not take effect")
	ErrClosed        = errors.New("store: already closed")
)

type Option func(*Store)

func WithClock(c domain.Clock) Option {
	return func(s *Store) {
		if c != nil {
			s.clock = c
		}
	}
}

func WithMaxOpenConns(n int) Option {
	return func(s *Store) {
		if n > 0 {
			s.maxOpen = n
		}
	}
}

type Store struct {
	*repoSet

	db      *sql.DB
	path    string
	sealer  secrets.Sealer
	clock   domain.Clock
	maxOpen int

	writeMu  sync.Mutex
	closeOne sync.Once
	closed   atomic.Bool
}

func Open(path string, k secrets.Sealer, opts ...Option) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, ErrPathMissing
	}
	if k == nil {
		return nil, ErrSealerMissing
	}

	s := &Store{path: path, sealer: k, clock: domain.SystemClock(), maxOpen: MaxOpenConns}
	for _, o := range opts {
		o(s)
	}

	if dir := filepath.Dir(path); dir != "" && dir != "." && !isMemoryPath(path) {
		if err := os.MkdirAll(dir, DirMode); err != nil {
			return nil, fmt.Errorf("store: cannot create %s: %w", dir, err)
		}
	}

	db, err := sql.Open("sqlite", DSN(path)+"&_txlock="+TxLock)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(s.maxOpen)
	db.SetMaxIdleConns(s.maxOpen)
	db.SetConnMaxLifetime(0)

	s.db = db
	s.repoSet = newRepoSet(db, k, s.now)

	if err := s.verifyPool(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func isMemoryPath(path string) bool {
	return strings.Contains(path, "mode=memory") || path == ":memory:"
}

func (s *Store) Path() string { return s.path }

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Sealer() secrets.Sealer { return s.sealer }

func (s *Store) now() int64 { return domain.UnixMillis(s.clock.Now()) }

func (s *Store) Close() error {
	var err error
	s.closeOne.Do(func() {
		s.closed.Store(true)
		err = s.db.Close()
	})
	return err
}

func (s *Store) verifyPool(ctx context.Context) error {
	held := make([]*sql.Conn, 0, s.maxOpen)
	defer func() {
		for _, c := range held {
			c.Close()
		}
	}()
	for i := 0; i < s.maxOpen; i++ {
		c, err := s.db.Conn(ctx)
		if err != nil {
			return fmt.Errorf("store: cannot open connection %d of %d: %w", i+1, s.maxOpen, err)
		}
		held = append(held, c)
		if err := verifyPragmas(ctx, c); err != nil {
			return err
		}
	}
	return nil
}

func verifyPragmas(ctx context.Context, c *sql.Conn) error {
	var journal string
	if err := c.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil {
		return fmt.Errorf("store: read journal_mode: %w", err)
	}
	if !strings.EqualFold(journal, PragmaJournalMode) && !strings.EqualFold(journal, "memory") {
		return fmt.Errorf("%w: journal_mode is %q, want %q", ErrPragma, journal, PragmaJournalMode)
	}

	var busy int
	if err := c.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busy); err != nil {
		return fmt.Errorf("store: read busy_timeout: %w", err)
	}
	if busy != PragmaBusyTimeout {
		return fmt.Errorf("%w: busy_timeout is %d, want %d", ErrPragma, busy, PragmaBusyTimeout)
	}

	var fk int
	if err := c.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
		return fmt.Errorf("store: read foreign_keys: %w", err)
	}
	if fk != 1 {
		return fmt.Errorf("%w: foreign_keys is off, a dead stick would orphan its slot silently", ErrPragma)
	}
	return nil
}

type Tx struct {
	*repoSet
	tx *sql.Tx
}

func (t *Tx) Unwrap() *sql.Tx { return t.tx }

func (s *Store) Tx(ctx context.Context, fn func(*Tx) error) error {
	if s.closed.Load() {
		return ErrClosed
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return mapErr(err, "begin transaction")
	}
	t := &Tx{repoSet: newRepoSet(tx, s.sealer, s.now), tx: tx}

	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()

	if err := fn(t); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return mapErr(err, "commit transaction")
	}
	committed = true
	return nil
}

func (s *Store) IntegrityCheck(ctx context.Context) error {
	return integrityCheck(ctx, s.db)
}

func integrityCheck(ctx context.Context, q queryer) error {
	rows, err := q.QueryContext(ctx, "PRAGMA integrity_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	problems := make([]string, 0, 1)
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return err
		}
		if !strings.EqualFold(line, "ok") {
			problems = append(problems, line)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(problems) > 0 {
		return fmt.Errorf("store: integrity_check reported %s", strings.Join(problems, "; "))
	}
	return nil
}

const (
	codeConstraint           = 19
	codeConstraintCheck      = 19 | (1 << 8)
	codeConstraintForeignKey = 19 | (3 << 8)
	codeConstraintNotNull    = 19 | (5 << 8)
	codeConstraintPrimaryKey = 19 | (6 << 8)
	codeConstraintUnique     = 19 | (8 << 8)
)

func mapErr(err error, what string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%s: %w", what, domain.ErrNotFound)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s: %w", what, err)
	}
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() {
		case codeConstraintUnique, codeConstraintPrimaryKey, codeConstraint:
			return fmt.Errorf("%s: %w: %v", what, domain.ErrConflict, err)
		case codeConstraintForeignKey:
			return fmt.Errorf("%s: %w: referenced row does not exist: %v", what, domain.ErrInvalid, err)
		case codeConstraintCheck, codeConstraintNotNull:
			return fmt.Errorf("%s: %w: %v", what, domain.ErrInvalid, err)
		}
	}
	if isConstraintText(err) {
		return fmt.Errorf("%s: %w: %v", what, domain.ErrConflict, err)
	}
	return fmt.Errorf("%s: %w", what, err)
}

func isConstraintText(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint failed") || strings.Contains(msg, "constraint failed")
}

func notFound(what, id string) error {
	return fmt.Errorf("%s %q: %w", what, id, domain.ErrNotFound)
}

func millis(t time.Time) int64 { return domain.UnixMillis(t) }
