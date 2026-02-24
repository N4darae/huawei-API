package files

import (
	"context"

	"github.com/n4darae/huawei-API/src/internal/netcfg"
)

type Command struct {
	Args           []string
	TolerateAbsent bool
}

func LinkCommands(iface string) []Command {
	return []Command{
		{Args: []string{"ip", "link", "set", iface, "down"}, TolerateAbsent: true},
		{Args: []string{"udevadm", "control", "--reload"}},
		{Args: []string{"udevadm", "trigger", "--subsystem-match=net", "--action=add"}},
		{Args: []string{"udevadm", "settle"}, TolerateAbsent: true},
	}
}

func NetworkCommands(iface string) []Command {
	return []Command{
		{Args: []string{"networkctl", "reload"}},
		{Args: []string{"networkctl", "reconfigure", iface}, TolerateAbsent: true},
	}
}

func Commands(iface string, c Changed) []Command {
	var out []Command
	if c.Link {
		out = append(out, LinkCommands(iface)...)
	}
	if c.Network {
		out = append(out, NetworkCommands(iface)...)
	}
	return out
}

type Reloader struct {
	Exec netcfg.Exec
}

func NewReloader(e netcfg.Exec) Reloader {
	if e == nil {
		e = netcfg.SystemExec
	}
	return Reloader{Exec: e}
}

func (r Reloader) Apply(ctx context.Context, iface string, c Changed) error {
	return r.run(ctx, Commands(iface, c))
}

func (r Reloader) ApplyLink(ctx context.Context, iface string) error {
	return r.run(ctx, LinkCommands(iface))
}

func (r Reloader) ApplyNetwork(ctx context.Context, iface string) error {
	return r.run(ctx, NetworkCommands(iface))
}

func (r Reloader) run(ctx context.Context, cmds []Command) error {
	for _, c := range cmds {
		if len(c.Args) == 0 {
			continue
		}
		_, err := r.Exec(ctx, c.Args[0], c.Args[1:]...)
		if err == nil {
			continue
		}
		if c.TolerateAbsent && netcfg.IsAbsent(err) {
			continue
		}
		return err
	}
	return nil
}
