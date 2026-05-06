package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/n4darae/huawei-API/src/internal/config"
	"github.com/n4darae/huawei-API/src/internal/domain"
)

const (
	BackupPrefix    = config.Product + "-"
	BackupExt       = ".db"
	BackupTimestamp = "20060102T150405Z"
	BackupDirMode   = 0o750
	BackupFileMode  = 0o600
)

var ErrBackupExists = errors.New("store: backup target already exists")

func (s *Store) Backup(ctx context.Context, dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("store: backup directory is required: %w", domain.ErrInvalid)
	}
	if err := os.MkdirAll(dir, BackupDirMode); err != nil {
		return "", fmt.Errorf("store: cannot create %s: %w", dir, err)
	}

	at := s.clock.Now().UTC()
	target, err := freeBackupPath(dir, at)
	if err != nil {
		return "", err
	}

	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, target); err != nil {
		return "", fmt.Errorf("store: VACUUM INTO %s: %w", target, err)
	}
	if err := os.Chmod(target, BackupFileMode); err != nil {
		return "", fmt.Errorf("store: cannot chmod %s: %w", target, err)
	}
	if err := VerifyBackup(ctx, target); err != nil {
		return "", err
	}
	if err := s.settings.Set(ctx, SettingLastBackupAt, strconv.FormatInt(millis(at), 10), millis(at)); err != nil {
		return "", err
	}
	return target, nil
}

func freeBackupPath(dir string, at time.Time) (string, error) {
	stamp := at.Format(BackupTimestamp)
	for i := 0; i < 100; i++ {
		name := BackupPrefix + stamp + BackupExt
		if i > 0 {
			name = BackupPrefix + stamp + "-" + strconv.Itoa(i) + BackupExt
		}
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
			return p, nil
		}
	}
	return "", fmt.Errorf("%w: %s already holds 100 backups for this second", ErrBackupExists, dir)
}

func VerifyBackup(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", DSN(path))
	if err != nil {
		return fmt.Errorf("store: cannot open backup %s: %w", path, err)
	}
	defer db.Close()
	if err := integrityCheck(ctx, db); err != nil {
		return fmt.Errorf("backup %s: %w", path, err)
	}
	return nil
}

func (s *Store) LastBackupAt(ctx context.Context) (time.Time, error) {
	v, err := s.settings.Get(ctx, SettingLastBackupAt)
	if errors.Is(err, domain.ErrNotFound) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	ms, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("store: setting %s holds %q: %w", SettingLastBackupAt, v, domain.ErrInvalid)
	}
	return domain.FromUnixMillis(ms), nil
}
