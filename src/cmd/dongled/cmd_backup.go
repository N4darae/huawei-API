package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/n4darae/huawei-API/src/internal/config"
	"github.com/n4darae/huawei-API/src/internal/enroll"
	"github.com/n4darae/huawei-API/src/internal/store"
)

type backupCmd struct {
	dir    string
	verify string
	list   bool
	asJSON bool
}

func init() {
	c := &backupCmd{}
	Register(Command{
		Name:  "backup",
		Usage: "snapshot the database and verify the snapshot",
		Flags: c.flags,
		Run:   c.run,
	})
}

func (c *backupCmd) flags(fs *flag.FlagSet) {
	fs.StringVar(&c.dir, "dir", config.BackupDir, "directory the snapshot is written to")
	fs.StringVar(&c.verify, "verify", "", "verify an existing snapshot instead of taking a new one")
	fs.BoolVar(&c.list, "list", false, "report the newest snapshot and its age")
	fs.BoolVar(&c.asJSON, "json", false, "emit the result as json")
}

func (c *backupCmd) run(ctx context.Context, cfg config.Config, args []string) error {
	if err := rejectArgs("backup", args); err != nil {
		return err
	}
	dir := c.dir
	if dir == config.BackupDir && cfg.BackupDir != "" {
		dir = cfg.BackupDir
	}

	if c.verify != "" {
		if err := store.VerifyBackup(ctx, c.verify); err != nil {
			return err
		}
		fmt.Printf("%s passes an integrity check\n", c.verify)
		return nil
	}

	if c.list {
		path, at, err := enroll.NewestBackup(dir)
		if err != nil {
			return err
		}
		age := time.Since(at).Round(time.Minute)
		if c.asJSON {
			return writeJSON(map[string]any{"path": path, "taken_at": at, "age_seconds": int(age.Seconds())})
		}
		fmt.Printf("%s taken %s ago\n", path, age)
		return nil
	}

	sealer, err := loadSealer(cfg)
	if err != nil {
		return err
	}
	s, err := store.Open(cfg.DBPath, sealer)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := s.Migrate(ctx); err != nil {
		return err
	}

	path, err := s.Backup(ctx, dir)
	if err != nil {
		return err
	}
	pruneBackups(dir, backupKeep())
	if c.asJSON {
		return writeJSON(map[string]any{"path": path})
	}
	fmt.Printf("%s written and verified\n", path)
	fmt.Printf("\nThe snapshot is useless without the key that decrypts the proxy passwords.\n")
	fmt.Printf("Copy %s off this machine together with it.\n", kekPath(cfg))
	return nil
}

func backupKeep() int {
	if v, ok := os.LookupEnv("DONGLED_BACKUP_KEEP"); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			return n
		}
	}
	return config.DefaultBackupKeep
}

func pruneBackups(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Printf("backup: prune skipped, cannot list %s: %v", dir, err)
		return
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), store.BackupPrefix) || !strings.HasSuffix(e.Name(), store.BackupExt) {
			continue
		}
		names = append(names, e.Name())
	}
	if len(names) <= keep {
		return
	}
	sort.Strings(names)
	for _, name := range names[:len(names)-keep] {
		p := filepath.Join(dir, name)
		if err := os.Remove(p); err != nil {
			log.Printf("backup: prune failed for %s: %v", p, err)
		}
	}
}
