package reconcile

import (
	"sync"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

type Generations struct {
	mu   sync.Mutex
	gen  map[string]uint64
	live map[string]string
}

func NewGenerations() *Generations {
	return &Generations{gen: map[string]uint64{}, live: map[string]string{}}
}

func (g *Generations) Bump(target string) uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.gen[target]++
	return g.gen[target]
}

func (g *Generations) Get(target string) uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.gen[target]
}

func (g *Generations) Sync(active map[string]domain.Operation) int {
	g.mu.Lock()
	defer g.mu.Unlock()

	changed := 0
	for target, op := range active {
		if g.live[target] != op.ID {
			g.live[target] = op.ID
			g.gen[target]++
			changed++
		}
	}
	for target := range g.live {
		if _, still := active[target]; still {
			continue
		}
		delete(g.live, target)
		g.gen[target]++
		changed++
	}
	return changed
}

func (g *Generations) Snapshot(actions []Action) map[string]uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()

	out := make(map[string]uint64, len(actions))
	for _, a := range actions {
		out[a.Target()] = g.gen[a.Target()]
	}
	return out
}

func (g *Generations) Fenced(a Action, snapshot map[string]uint64) bool {
	target := a.Target()
	want, ok := snapshot[target]
	if !ok {
		return true
	}
	return g.Get(target) != want
}
