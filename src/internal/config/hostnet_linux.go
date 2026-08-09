package config

var (
	NetworkDir   = "/etc/systemd/network"
	RtTablesDir  = "/etc/iproute2/rt_tables.d"
	RtTablesFile = RtTablesDir + "/" + Product + ".conf"
)
