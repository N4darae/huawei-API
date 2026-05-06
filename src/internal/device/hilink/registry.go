package hilink

import (
	"context"
	"net/netip"
	"sync"

	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/domain"
)

type RegistryOptions struct {
	Options
	BaseURLFor func(addr netip.Addr) string
}

type Registry struct {
	mu      sync.Mutex
	opt     RegistryOptions
	clients map[netip.Addr]*Client
	closed  bool
}

func NewRegistry(opt RegistryOptions) *Registry {
	return &Registry{opt: opt, clients: map[netip.Addr]*Client{}}
}

func (r *Registry) ForSlot(ctx context.Context, s domain.Slot) (device.Device, error) {
	if !s.Valid() {
		return nil, domain.Wrap(domain.ErrInvalid, "hilink: slot %d", int(s))
	}
	return r.ForAddr(ctx, s.GatewayIP())
}

func (r *Registry) ForAddr(_ context.Context, addr netip.Addr) (device.Device, error) {
	if !addr.IsValid() {
		return nil, domain.Wrap(domain.ErrInvalid, "hilink: invalid address")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, domain.Wrap(domain.ErrUnreachable, "hilink: registry closed")
	}
	if c, ok := r.clients[addr]; ok {
		return c, nil
	}
	opt := r.opt.Options
	if r.opt.BaseURLFor != nil {
		opt.BaseURL = r.opt.BaseURLFor(addr)
	} else {
		opt.BaseURL = ""
	}
	c := New(addr, opt)
	r.clients[addr] = c
	return c, nil
}

func (r *Registry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	for _, c := range r.clients {
		c.hc.CloseIdleConnections()
	}
	r.clients = map[netip.Addr]*Client{}
	return nil
}

var _ device.Registry = (*Registry)(nil)

var _ device.Device = (*Client)(nil)
