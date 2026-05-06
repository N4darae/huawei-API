package sim

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"time"

	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/device/hilink"
	"github.com/n4darae/huawei-API/src/internal/domain"
)

type FarmOptions struct {
	FaultRate         float64
	Faults            FaultProfile
	HoldToNewIP       time.Duration
	Carrier           string
	Seed              int64
	Clock             func() time.Time
	TokenTTL          time.Duration
	MaxHang           time.Duration
	SlowResponse      time.Duration
	FactoryDefaultLAN bool
}

type Farm struct {
	mu      sync.Mutex
	opt     FarmOptions
	offset  time.Duration
	base    func() time.Time
	devices map[domain.Slot]*SimDevice
	servers map[domain.Slot]*httptest.Server
	byAddr  map[netip.Addr]domain.Slot
	slots   []domain.Slot
}

func NewFarm(n int, opt FarmOptions) *Farm {
	if opt.HoldToNewIP <= 0 {
		opt.HoldToNewIP = DefaultHoldToNewIP
	}
	if opt.Carrier == "" {
		opt.Carrier = DefaultCarrier
	}
	if opt.MaxHang <= 0 {
		opt.MaxHang = DefaultMaxHang
	}
	if opt.Clock == nil {
		opt.Clock = time.Now
	}
	if n < 0 {
		n = 0
	}
	if n > domain.MaxSlots {
		n = domain.MaxSlots
	}

	f := &Farm{
		opt:     opt,
		base:    opt.Clock,
		devices: map[domain.Slot]*SimDevice{},
		servers: map[domain.Slot]*httptest.Server{},
		byAddr:  map[netip.Addr]domain.Slot{},
	}
	for i := 1; i <= n; i++ {
		f.add(domain.Slot(i))
	}
	return f
}

func (f *Farm) add(slot domain.Slot) {
	st := newState(slot, f.opt.Carrier)
	if !f.opt.FactoryDefaultLAN {
		lan := slot.GatewayIP()
		st.dhcp.DHCPIPAddress = lan
		st.dhcp.PrimaryDNS = lan
		st.dhcp.SecondaryDNS = lan
		st.dhcp.DHCPStartIPAddress = withLastOctet(lan, 100)
		st.dhcp.DHCPEndIPAddress = withLastOctet(lan, 200)
	}
	d := &SimDevice{
		st:          st,
		tok:         newTokenStore(f.now, f.opt.TokenTTL),
		faults:      newFaultInjector(f.opt.Seed+int64(slot), f.opt.FaultRate, f.opt.Faults, f.opt.SlowResponse),
		now:         f.now,
		holdToNewIP: f.opt.HoldToNewIP,
		maxHang:     f.opt.MaxHang,
	}
	d.onMove = f.rebind
	srv := httptest.NewServer(http.HandlerFunc(d.ServeHTTP))
	f.devices[slot] = d
	f.servers[slot] = srv
	f.slots = append(f.slots, slot)
	f.byAddr[st.dhcp.DHCPIPAddress] = slot
}

func withLastOctet(a netip.Addr, last byte) netip.Addr {
	if !a.Is4() {
		return a
	}
	b := a.As4()
	b[3] = last
	return netip.AddrFrom4(b)
}

func (f *Farm) rebind(old, cur netip.Addr, d *SimDevice) {
	f.mu.Lock()
	defer f.mu.Unlock()
	slot, ok := f.byAddr[old]
	if !ok || f.devices[slot] != d {
		for s, dev := range f.devices {
			if dev == d {
				slot, ok = s, true
				break
			}
		}
	}
	if !ok {
		return
	}
	delete(f.byAddr, old)
	f.byAddr[cur] = slot
}

func (f *Farm) Slots() []domain.Slot {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.Slot, len(f.slots))
	copy(out, f.slots)
	return out
}

func (f *Farm) Device(slot domain.Slot) *SimDevice {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.devices[slot]
}

func (f *Farm) BaseURL(slot domain.Slot) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.servers[slot]; ok {
		return s.URL
	}
	return ""
}

func (f *Farm) BaseURLForAddr(addr netip.Addr) string {
	f.mu.Lock()
	slot, ok := f.byAddr[addr]
	srv := f.servers[slot]
	f.mu.Unlock()
	if !ok || srv == nil {
		return ""
	}
	return srv.URL
}

func (f *Farm) Registry() device.Registry {
	return hilink.NewRegistry(hilink.RegistryOptions{
		Options:    hilink.Options{Timeout: hilink.DefaultTimeout},
		BaseURLFor: f.BaseURLForAddr,
	})
}

func (f *Farm) Client(slot domain.Slot) *hilink.Client {
	url := f.BaseURL(slot)
	if url == "" {
		return nil
	}
	return hilink.New(slot.GatewayIP(), hilink.Options{BaseURL: url, Timeout: hilink.DefaultTimeout})
}

func (f *Farm) Now() time.Time { return f.now() }

func (f *Farm) now() time.Time {
	f.mu.Lock()
	off := f.offset
	base := f.base
	f.mu.Unlock()
	return base().Add(off)
}

func (f *Farm) Advance(d time.Duration) {
	f.mu.Lock()
	f.offset += d
	f.mu.Unlock()
}

func (f *Farm) Close() error {
	f.mu.Lock()
	servers := make([]*httptest.Server, 0, len(f.servers))
	for _, s := range f.servers {
		servers = append(servers, s)
	}
	f.servers = map[domain.Slot]*httptest.Server{}
	f.devices = map[domain.Slot]*SimDevice{}
	f.byAddr = map[netip.Addr]domain.Slot{}
	f.slots = nil
	f.mu.Unlock()
	for _, s := range servers {
		s.Close()
	}
	return nil
}
