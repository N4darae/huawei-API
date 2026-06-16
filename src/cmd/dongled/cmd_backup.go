package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/n4darae/huawei-API/src/internal/config"
	"github.com/n4darae/huawei-API/src/internal/enroll"
	"github.com/n4darae/huawei-API/src/internal/store"
)

func init() {
	Register(Command{
		Name:  "backup",
		Usage: "snapshot the database and verify the snapshot",
		Run:   runBackup,
	})
}

func runBackup(ctx context.Context, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet(config.Product+" backup", flag.ContinueOnError)
	dir := fs.String("dir", cfg.BackupDir, "directory the snapshot is written to")
	verify := fs.String("verify", "", "verify an existing snapshot instead of taking a new one")
	list := fs.Bool("list", false, "report the newest snapshot and its age")
	asJSON := fs.Bool("json", false, "emit the result as json")
	if err := parseSubFlags(fs, args); err != nil {
		return err
	}

	if *verify != "" {
		if err := store.VerifyBackup(ctx, *verify); err != nil {
			return err
		}
		fmt.Printf("%s passes an integrity check\n", *verify)
		return nil
	}

	if *list {
		path, at, err := enroll.NewestBackup(*dir)
		if err != nil {
			return err
		}
		age := time.Since(at).Round(time.Minute)
		if *asJSON {
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

	path, err := s.Backup(ctx, *dir)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(map[string]any{"path": path})
	}
	fmt.Printf("%s written and verified\n", path)
	fmt.Printf("\nThe snapshot is useless without the key that decrypts the proxy passwords.\n")
	fmt.Printf("Copy %s off this machine together with it.\n", kekPath(cfg))
	return nil
}
