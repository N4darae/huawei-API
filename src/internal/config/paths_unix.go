//go:build unix

package config

var (
	EtcDir    = "/etc/" + Product
	RunDir    = "/run/" + Product
	StateDir  = "/var/lib/" + Product
	LogDir    = "/var/log/" + Product
	BackupDir = "/var/backups/" + Product
	BinDir    = "/usr/local/lib/" + Product
)
