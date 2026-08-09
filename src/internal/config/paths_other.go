//go:build !unix

package config

import (
	"os"
	"strings"
)

var (
	EtcDir    = programDataDir() + "/etc"
	RunDir    = programDataDir() + "/run"
	StateDir  = programDataDir() + "/state"
	LogDir    = programDataDir() + "/log"
	BackupDir = programDataDir() + "/backup"
	BinDir    = programDataDir() + "/lib"
)

func programDataDir() string {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = "C:/ProgramData"
	}
	return strings.ReplaceAll(base, "\\", "/") + "/" + Product
}
