package store

import (
	"embed"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

const MigrationsDir = "migrations"

type Migration struct {
	Version int
	Name    string
	SQL     string
}

func MigrationsFS() fs.FS { return migrationsFS }

func Migrations() ([]Migration, error) {
	entries, err := fs.ReadDir(migrationsFS, MigrationsDir)
	if err != nil {
		return nil, err
	}
	out := make([]Migration, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		version, name, ok := strings.Cut(strings.TrimSuffix(e.Name(), ".sql"), "_")
		if !ok {
			return nil, domain.Wrap(domain.ErrInvalid, "migration %q is not named <version>_<name>.sql", e.Name())
		}
		v, err := strconv.Atoi(version)
		if err != nil {
			return nil, domain.Wrap(domain.ErrInvalid, "migration %q has a non numeric version", e.Name())
		}
		body, err := fs.ReadFile(migrationsFS, MigrationsDir+"/"+e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, Migration{Version: v, Name: name, SQL: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	for i := range out {
		if out[i].Version != i+1 {
			return nil, domain.Wrap(domain.ErrInvalid, "migration versions must be contiguous from 1, found %d at position %d", out[i].Version, i+1)
		}
	}
	return out, nil
}

const (
	PragmaJournalMode = "WAL"
	PragmaBusyTimeout = 5000
	PragmaForeignKeys = "ON"
	PragmaSynchronous = "NORMAL"
)

func DSN(path string) string {
	return "file:" + path +
		"?_pragma=journal_mode(" + PragmaJournalMode + ")" +
		"&_pragma=busy_timeout(" + strconv.Itoa(PragmaBusyTimeout) + ")" +
		"&_pragma=foreign_keys(" + PragmaForeignKeys + ")" +
		"&_pragma=synchronous(" + PragmaSynchronous + ")"
}
