package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/netip"
	"os"
	"strings"

	"github.com/n4darae/huawei-API/src/internal/config"
	"github.com/n4darae/huawei-API/src/internal/enroll"
)

func init() {
	Register(Command{
		Name:  "preflight",
		Usage: "check host readiness, read only (subcommand flags go after --)",
		Run:   runPreflight,
	})
}

func runPreflight(ctx context.Context, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet(config.Product+" preflight", flag.ContinueOnError)
	fatalOnly := fs.Bool("fatal-only", false, "exit non-zero only when a fatal check fails")
	asJSON := fs.Bool("json", false, "emit the report as json")
	quiet := fs.Bool("quiet", false, "print nothing, report through the exit code")
	if err := parseSubFlags(fs, args); err != nil {
		return err
	}

	report := enroll.Preflight(ctx, preflightOptions(cfg))

	switch {
	case *quiet:
	case *asJSON:
		if err := writeJSON(report); err != nil {
			return err
		}
	default:
		fmt.Print(report.Text())
	}

	if report.Green(*fatalOnly) {
		return nil
	}
	failed := report.Failed()
	if *fatalOnly {
		failed = report.FatalFailed()
	}
	names := make([]string, 0, len(failed))
	for _, c := range failed {
		names = append(names, c.Name)
	}
	return fmt.Errorf("preflight: %d check(s) failed: %s", len(failed), strings.Join(names, ", "))
}

func preflightOptions(cfg config.Config) enroll.PreflightOptions {
	o := enroll.PreflightOptions{
		Bin3proxy:   cfg.Bin3proxy,
		BinDir:      cfg.BinDir,
		BackupDir:   cfg.BackupDir,
		PanelAddr:   cfg.PanelAddr,
		MetricsAddr: cfg.MetricsAddr,
	}
	if cfg.PublicHost.IsValid() {
		o.PublicHosts = []netip.Addr{cfg.PublicHost}
	}
	return o
}

func parseSubFlags(fs *flag.FlagSet, args []string) error {
	fs.SetOutput(os.Stderr)
	return fs.Parse(normalizeSubFlags(args))
}

func normalizeSubFlags(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			out = append(out, a)
			continue
		}
		if k, _, ok := strings.Cut(a, "="); ok && k != "" && isFlagName(k) {
			out = append(out, "-"+a)
			continue
		}
		out = append(out, a)
	}
	return out
}

func isFlagName(s string) bool {
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9' && i > 0:
		case r == '-' && i > 0:
		default:
			return false
		}
	}
	return s != ""
}

func writeJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
