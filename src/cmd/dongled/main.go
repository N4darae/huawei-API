package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"sort"
	"strings"
	"syscall"

	"github.com/n4darae/huawei-API/src/internal/config"
)

type Command struct {
	Name  string
	Usage string
	Flags func(fs *flag.FlagSet)
	Run   func(ctx context.Context, cfg config.Config, args []string) error
}

var commands = map[string]Command{}

func Register(c Command) {
	if _, dup := commands[c.Name]; dup {
		panic("duplicate subcommand " + c.Name)
	}
	commands[c.Name] = c
}

func init() {
	Register(Command{Name: "serve", Usage: "run the panel and the reconcile engine", Run: runServe})
	Register(Command{Name: "version", Usage: "print build information", Run: runVersion})
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, config.Product+":", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, argv []string) error {
	if len(argv) == 0 || argv[0] == "-h" || argv[0] == "--help" || argv[0] == "help" {
		usage()
		return nil
	}
	name := argv[0]
	cmd, ok := commands[name]
	if !ok {
		usage()
		return fmt.Errorf("unknown subcommand %q", name)
	}

	cfg := config.Default()
	if err := cfg.ApplyEnv(os.LookupEnv); err != nil {
		return err
	}

	fs := flag.NewFlagSet(config.Product+" "+name, flag.ContinueOnError)
	cfg.BindFlags(fs)
	if cmd.Flags != nil {
		cmd.Flags(fs)
	}
	if err := fs.Parse(argv[1:]); err != nil {
		return err
	}
	return cmd.Run(ctx, cfg, fs.Args())
}

func usage() {
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)

	var b strings.Builder
	fmt.Fprintf(&b, "usage: %s <command> [flags]\n\ncommands:\n", config.Product)
	for _, n := range names {
		fmt.Fprintf(&b, "  %-12s %s\n", n, commands[n].Usage)
	}
	fmt.Fprint(os.Stderr, b.String())
}

func runServe(ctx context.Context, cfg config.Config, _ []string) error {
	app, err := Wire(ctx, cfg)
	if err != nil {
		return err
	}
	return app.Run(ctx)
}

func runVersion(_ context.Context, _ config.Config, _ []string) error {
	fmt.Println(config.Product, buildVersion())
	return nil
}

func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			return s.Value
		}
	}
	return info.Main.Version
}
