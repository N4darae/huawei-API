package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/n4darae/huawei-API/src/internal/config"
	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/enroll"
	"github.com/n4darae/huawei-API/src/internal/netcfg/files"
	"github.com/n4darae/huawei-API/src/internal/proxysup"
	"github.com/n4darae/huawei-API/src/internal/secrets"
)

const DefaultDeploySource = "/usr/local/share/" + config.Product + "/deploy"

func init() {
	Register(Command{
		Name:  "bootstrap",
		Usage: "lay down directories, units and host config (prints a plan unless --apply)",
		Run:   runBootstrap,
	})
}

type action struct {
	Path   string `json:"path"`
	What   string `json:"what"`
	Mode   string `json:"mode"`
	Change bool   `json:"change"`
	body   []byte
	mode   os.FileMode
	dir    bool
}

func runBootstrap(_ context.Context, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet(config.Product+" bootstrap", flag.ContinueOnError)
	root := fs.String("root", "", "prefix every path with this directory, for testing an install in a chroot or temp dir")
	source := fs.String("source", DefaultDeploySource, "directory holding the deploy/ tree")
	apply := fs.Bool("apply", false, "actually write; the default only prints the plan")
	force := fs.Bool("force", false, "allow --apply against the live filesystem, needed when --root is empty")
	asJSON := fs.Bool("json", false, "emit the plan as json")
	if err := parseSubFlags(fs, args); err != nil {
		return err
	}
	if *apply && *root == "" && !*force {
		return errors.New("bootstrap: --apply without --root writes into /etc, /usr and /var of this machine. Pass --root DIR to rehearse it first, or --force if this really is the farm host")
	}

	plan, err := buildPlan(cfg, *root, *source)
	if err != nil {
		return err
	}

	if !*apply {
		if *asJSON {
			return writeJSON(plan)
		}
		fmt.Printf("plan only, nothing was written. Re-run with -- --apply to execute.\n\n")
		printPlan(plan)
		printNextSteps(*root)
		return nil
	}

	for _, a := range plan {
		if err := execAction(a); err != nil {
			return err
		}
	}
	if *asJSON {
		return writeJSON(plan)
	}
	printPlan(plan)
	printNextSteps(*root)
	return nil
}

func buildPlan(cfg config.Config, root, source string) ([]action, error) {
	at := func(p string) string {
		if root == "" {
			return p
		}
		return filepath.Join(root, p)
	}

	var plan []action
	for _, d := range []struct {
		path string
		mode os.FileMode
	}{
		{cfg.EtcDir, 0o750},
		{cfg.EtcDir + "/proxy", 0o750},
		{cfg.StateDir, 0o750},
		{cfg.LogDir, 0o750},
		{cfg.BackupDir, 0o750},
		{cfg.BinDir, 0o755},
		{config.RtTablesDir, 0o755},
		{cfg.NetworkDir, 0o755},
	} {
		plan = append(plan, action{Path: at(d.path), What: "directory", Mode: d.mode.String(), dir: true, mode: d.mode})
	}

	rtFile := at(config.RtTablesFile)
	plan = append(plan, action{
		Path: rtFile,
		What: "route table names for all " + fmt.Sprint(domain.MaxSlots) + " slots",
		Mode: "0644",
		body: files.RenderRouteTables(domain.Slots()),
		mode: 0o644,
	})

	plan = append(plan, action{
		Path: at("/etc/systemd/system/" + config.UnitProxyTpl),
		What: "rendered by proxysup.RenderUnit, never a hand written copy",
		Mode: "0644",
		body: proxysup.RenderUnit(proxysup.UnitOptions{
			Bin:     cfg.Bin3proxy,
			ConfDir: cfg.EtcDir + "/proxy",
			LogDir:  cfg.LogDir,
		}),
		mode: 0o644,
	})

	for _, c := range []struct{ from, to string }{
		{"dongled.service", "/etc/systemd/system/" + config.UnitBackend},
		{"sysusers.d/" + config.Product + ".conf", "/etc/sysusers.d/" + config.Product + ".conf"},
		{"sysctl.d/60-" + config.Product + ".conf", "/etc/sysctl.d/60-" + config.Product + ".conf"},
		{"udev/" + enroll.UdevRuleName, "/etc/udev/rules.d/" + enroll.UdevRuleName},
		{"networkd.conf.d/" + config.Product + ".conf", "/etc/systemd/networkd.conf.d/" + config.Product + ".conf"},
		{"nginx/conf.d/" + config.Product + "-limits.conf", "/etc/nginx/conf.d/" + config.Product + "-limits.conf"},
		{"preflight.sh", "/usr/local/share/" + config.Product + "/preflight.sh"},
	} {
		body, err := os.ReadFile(filepath.Join(source, c.from))
		if err != nil {
			return nil, fmt.Errorf("bootstrap: %s is not in %s: %w", c.from, source, err)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(c.from, ".sh") {
			mode = 0o755
		}
		plan = append(plan, action{Path: at(c.to), What: "from " + c.from, Mode: mode.String(), body: body, mode: mode})
	}

	if sum, err := enroll.FileSHA256(cfg.Bin3proxy); err == nil {
		pin := enroll.Pin{SHA256: sum, Commit: config.Pin3proxyCommit}
		plan = append(plan, action{
			Path: at(enroll.PinPath(cfg.BinDir)),
			What: "digest of the pinned 3proxy build",
			Mode: "0644",
			body: []byte(pin.String() + "\n"),
			mode: 0o644,
		})
	}

	plan = append(plan, action{Path: at(config.FarmMarker), What: "marks this host as the farm; enroll refuses without it", Mode: "0644", body: []byte("farm\n"), mode: 0o644})

	for i := range plan {
		plan[i].Change = needsChange(plan[i])
	}
	return plan, nil
}

func needsChange(a action) bool {
	if a.dir {
		st, err := os.Stat(a.Path)
		return err != nil || !st.IsDir()
	}
	got, err := os.ReadFile(a.Path)
	if err != nil {
		return true
	}
	return string(got) != string(a.body)
}

func execAction(a action) error {
	if a.dir {
		return os.MkdirAll(a.Path, a.mode)
	}
	if !a.Change {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(a.Path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(a.Path, a.body, a.mode)
}

func printPlan(plan []action) {
	width := 0
	for _, a := range plan {
		if len(a.Path) > width {
			width = len(a.Path)
		}
	}
	for _, a := range plan {
		mark := "keep"
		if a.Change {
			mark = "write"
		}
		fmt.Printf("%-5s %-*s  %s\n", mark, width, a.Path, a.What)
	}
}

func printNextSteps(root string) {
	if root != "" {
		fmt.Printf("\nRehearsed under %s. Nothing outside it was touched.\n", root)
		return
	}
	fmt.Printf(`
Nothing above created an account, changed a sysctl, or restarted a service.
Those are yours to run, in this order, and each one is reversible:

  1. systemd-sysusers                          creates group %s (gid %d)
                                               and px01..px%02d
  2. sysctl --system                           applies %s
  3. udevadm control --reload-rules && udevadm trigger --subsystem-match=usb
  4. systemctl restart systemd-networkd        picks up ManageForeignRoutingPolicyRules=no
  5. %s bootstrap-kek                          see docs/INSTALL.md for the key ceremony
  6. systemctl daemon-reload
  7. %s preflight                              must be green before step 8
  8. systemctl enable --now %s
  9. nginx -t && systemctl reload nginx        never restart

Step 4 restarts networking. Do it from the console or a tmux session, not over
the ssh connection you need to keep.
`, config.GroupName, config.GroupGID, domain.MaxSlots,
		"/etc/sysctl.d/60-"+config.Product+".conf", config.Product, config.Product, config.UnitBackend)
}

func init() {
	Register(Command{
		Name:  "bootstrap-kek",
		Usage: "generate the key that encrypts proxy passwords at rest, once",
		Run:   runBootstrapKEK,
	})
}

func runBootstrapKEK(_ context.Context, cfg config.Config, args []string) error {
	fset := flag.NewFlagSet(config.Product+" bootstrap-kek", flag.ContinueOnError)
	path := fset.String("path", "", "where the key is written, defaults to "+config.KEKCredFile)
	force := fset.Bool("force", false, "overwrite an existing key, which makes every stored password unreadable")
	if err := parseSubFlags(fset, args); err != nil {
		return err
	}
	target := *path
	if target == "" {
		target = kekPath(cfg)
	}
	if _, err := os.Stat(target); err == nil && !*force {
		return fmt.Errorf("bootstrap-kek: %s already exists. Overwriting it makes every stored proxy password permanently unreadable; pass --force only if you have accepted that", target)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	kek, err := secrets.GenerateKEK()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}
	if err := secrets.WriteKEK(target, kek); err != nil {
		return err
	}
	fmt.Printf("%s written, mode 0600\n", target)
	fmt.Printf(`
This file is the only thing that can decrypt the proxy passwords in
%s. A database backup without it is worthless.

Copy it somewhere off this machine now, before you enrol anything, and confirm
that the copy is readable. There is no recovery path.
`, cfg.DBPath)
	return nil
}
