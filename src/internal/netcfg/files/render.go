package files

import (
	"bytes"
	_ "embed"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/n4darae/huawei-API/src/internal/config"
	"github.com/n4darae/huawei-API/src/internal/domain"
	"github.com/n4darae/huawei-API/src/internal/netcfg"
)

//go:embed link.tmpl
var linkTemplateSource string

//go:embed network.tmpl
var networkTemplateSource string

var (
	linkTemplate    = template.Must(template.New("link").Parse(linkTemplateSource))
	networkTemplate = template.Must(template.New("network").Parse(networkTemplateSource))
)

type linkView struct {
	IDPath string
	IfName string
}

type networkView struct {
	IfName       string
	HostPrefix   string
	HostPrefix32 string
	Subnet       string
	Gateway      string
	Table        int
	UID          int
	PrioSrc      int
	PrioUID      int
}

type Changed struct {
	Link    bool
	Network bool
}

func (c Changed) Any() bool { return c.Link || c.Network }

func RenderLink(s domain.Slot, idPath string) ([]byte, error) {
	if !s.Valid() {
		return nil, netcfg.ErrInvalidSlot
	}
	if strings.TrimSpace(idPath) == "" {
		return nil, netcfg.ErrNoIDPath
	}
	var buf bytes.Buffer
	if err := linkTemplate.Execute(&buf, linkView{IDPath: idPath, IfName: s.IfaceName()}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func RenderNetwork(s domain.Slot) ([]byte, error) {
	if !s.Valid() {
		return nil, netcfg.ErrInvalidSlot
	}
	v := networkView{
		IfName:       s.IfaceName(),
		HostPrefix:   s.HostPrefix().String(),
		HostPrefix32: netip.PrefixFrom(s.HostIP(), s.HostIP().BitLen()).String(),
		Subnet:       s.Subnet().String(),
		Gateway:      s.GatewayIP().String(),
		Table:        s.RouteTable(),
		UID:          s.UID(),
		PrioSrc:      s.RulePrioSrc(),
		PrioUID:      s.RulePrioUID(),
	}
	var buf bytes.Buffer
	if err := networkTemplate.Execute(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func RenderRouteTables(slots []domain.Slot) []byte {
	ordered := make([]domain.Slot, 0, len(slots))
	for _, s := range slots {
		if s.Valid() {
			ordered = append(ordered, s)
		}
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	var buf bytes.Buffer
	for _, s := range ordered {
		fmt.Fprintf(&buf, "%d\t%s\n", s.RouteTable(), s.RouteTableName())
	}
	return buf.Bytes()
}

type Renderer struct {
	Dir string
}

func NewRenderer(dir string) *Renderer { return &Renderer{Dir: dir} }

func (r *Renderer) LinkPath(s domain.Slot) string {
	return filepath.Join(r.Dir, s.LinkFileName())
}

func (r *Renderer) NetworkPath(s domain.Slot) string {
	return filepath.Join(r.Dir, s.NetworkFileName())
}

func (r *Renderer) WriteSlot(s domain.Slot, idPath string) (Changed, error) {
	var c Changed
	link, err := RenderLink(s, idPath)
	if err != nil {
		return c, err
	}
	network, err := RenderNetwork(s)
	if err != nil {
		return c, err
	}
	c.Network, err = writeIfChanged(r.NetworkPath(s), network)
	if err != nil {
		return c, err
	}
	c.Link, err = writeIfChanged(r.LinkPath(s), link)
	if err != nil {
		return c, err
	}
	return c, nil
}

func (r *Renderer) RemoveSlot(s domain.Slot) (Changed, error) {
	if !s.Valid() {
		return Changed{}, netcfg.ErrInvalidSlot
	}
	linkGone, err := removeIfPresent(r.LinkPath(s))
	if err != nil {
		return Changed{}, err
	}
	networkGone, err := removeIfPresent(r.NetworkPath(s))
	if err != nil {
		return Changed{}, err
	}
	return Changed{Link: linkGone, Network: networkGone}, nil
}

func WriteRouteTables(path string, slots []domain.Slot) (bool, error) {
	return writeIfChanged(path, RenderRouteTables(slots))
}

func RouteTablesComplete(path string, slots []domain.Slot) bool {
	got, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return bytes.Equal(bytes.TrimSpace(got), bytes.TrimSpace(RenderRouteTables(slots)))
}

func writeIfChanged(path string, want []byte) (bool, error) {
	if got, err := os.ReadFile(path); err == nil && bytes.Equal(got, want) {
		return false, nil
	} else if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp")
	if err != nil {
		return false, err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(want); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(name, path); err != nil {
		return false, err
	}
	return true, nil
}

func removeIfPresent(path string) (bool, error) {
	err := os.Remove(path)
	switch {
	case err == nil:
		return true, nil
	case os.IsNotExist(err):
		return false, nil
	default:
		return false, err
	}
}

func DefaultNetworkDir() string { return config.NetworkDir }

func DefaultRouteTablesFile() string { return config.RtTablesFile }
