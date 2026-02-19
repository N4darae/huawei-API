package eventbus

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

const DefaultSubscriberBuffer = 256

type subscriber struct {
	ch     chan Event
	topics map[string]struct{}
	once   sync.Once
}

func (s *subscriber) wants(topic string) bool {
	if _, ok := s.topics[TopicAll]; ok {
		return true
	}
	_, ok := s.topics[topic]
	return ok
}

func (s *subscriber) close() {
	s.once.Do(func() { close(s.ch) })
}

type MemBus struct {
	buffer int

	mu     sync.Mutex
	subs   map[*subscriber]struct{}
	closed bool

	published atomic.Uint64
	lagged    atomic.Uint64
}

var _ Bus = (*MemBus)(nil)

func NewMemBus(buffer int) *MemBus {
	if buffer <= 0 {
		buffer = DefaultSubscriberBuffer
	}
	return &MemBus{buffer: buffer, subs: map[*subscriber]struct{}{}}
}

func (b *MemBus) Publish(ctx context.Context, e Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !e.Type.Valid() {
		return domain.Wrap(domain.ErrInvalid, "eventbus: unknown event type %q", string(e.Type))
	}
	if e.Topic == "" {
		e.Topic = e.Type.Topic()
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return domain.Wrap(domain.ErrConflict, "eventbus: closed")
	}
	var lagging []*subscriber
	for s := range b.subs {
		if !s.wants(e.Topic) {
			continue
		}
		select {
		case s.ch <- e:
		default:
			lagging = append(lagging, s)
		}
	}
	for _, s := range lagging {
		delete(b.subs, s)
	}
	b.mu.Unlock()

	b.published.Add(1)
	for _, s := range lagging {
		b.lagged.Add(1)
		s.close()
	}
	return nil
}

func (b *MemBus) Subscribe(ctx context.Context, topics []string) (<-chan Event, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	set := map[string]struct{}{}
	for _, t := range topics {
		set[t] = struct{}{}
	}
	if len(set) == 0 {
		set[TopicAll] = struct{}{}
	}

	s := &subscriber{ch: make(chan Event, b.buffer), topics: set}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, nil, domain.Wrap(domain.ErrConflict, "eventbus: closed")
	}
	b.subs[s] = struct{}{}
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		_, live := b.subs[s]
		delete(b.subs, s)
		b.mu.Unlock()
		if live {
			s.close()
		}
	}
	return s.ch, cancel, nil
}

func (b *MemBus) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	subs := make([]*subscriber, 0, len(b.subs))
	for s := range b.subs {
		subs = append(subs, s)
	}
	b.subs = map[*subscriber]struct{}{}
	b.mu.Unlock()
	for _, s := range subs {
		s.close()
	}
}

func (b *MemBus) Subscribers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}

func (b *MemBus) Published() uint64 { return b.published.Load() }

func (b *MemBus) Lagged() uint64 { return b.lagged.Load() }
