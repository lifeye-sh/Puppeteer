// Package listener coordinates prepared TCP listeners without cancelling the
// established connections of an older configuration generation.
package listener

import (
	"context"
	"errors"
	"net"
	"sync"
)

var ErrStale = errors.New("listener preparation is no longer current")

type Handler func(context.Context, net.Conn)
type Spec struct {
	Name, Address, Identity string
	Handle                  Handler
}
type State struct {
	Name     string `json:"name"`
	Address  string `json:"address"`
	Identity string `json:"identity"`
}
type generation struct {
	listener net.Listener
	spec     Spec // protected by Manager.mu; captured separately for each connection
}
type Manager struct {
	mu          sync.Mutex
	ctx         context.Context
	cancel      context.CancelFunc
	version     uint64
	closed      bool
	active      map[string]*generation
	connections map[net.Conn]bool
	slots       chan struct{}
	wg          sync.WaitGroup
}
type Prepared struct {
	manager  *Manager
	base     uint64
	next     map[string]*generation
	specs    map[string]Spec
	created  []*generation
	finished bool
}

func New(ctx context.Context, maxConnections int) *Manager {
	if maxConnections < 1 {
		maxConnections = 64
	}
	life, cancel := context.WithCancel(ctx)
	m := &Manager{ctx: life, cancel: cancel, active: map[string]*generation{}, connections: map[net.Conn]bool{}, slots: make(chan struct{}, maxConnections)}
	context.AfterFunc(life, m.Close)
	return m
}

// Prepare binds all changed endpoints before altering any active listener.
// A certificate or host key must already be validated by the Spec's caller.
// Blank Address means disabled. All listeners not in specs are removed on commit.
func (m *Manager) Prepare(ctx context.Context, specs []Spec) (*Prepared, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.ctx.Err() != nil {
		return nil, ErrStale
	}
	p := &Prepared{manager: m, base: m.version, next: map[string]*generation{}, specs: map[string]Spec{}}
	rollback := func() {
		for _, g := range p.created {
			g.listener.Close()
		}
	}
	for _, s := range specs {
		if s.Name == "" || p.specs[s.Name].Name != "" || (s.Address != "" && s.Handle == nil) {
			rollback()
			return nil, errors.New("invalid listener specification")
		}
		p.specs[s.Name] = s
		if s.Address == "" {
			continue
		}
		if old := m.active[s.Name]; old != nil && old.spec.Address == s.Address {
			p.next[s.Name] = old
			continue
		}
		l, err := (&net.ListenConfig{}).Listen(ctx, "tcp", s.Address)
		if err != nil {
			rollback()
			return nil, err
		}
		g := &generation{listener: l, spec: s}
		p.created = append(p.created, g)
		p.next[s.Name] = g
	}
	if ctx.Err() != nil {
		rollback()
		return nil, ctx.Err()
	}
	return p, nil
}

// Abort is safe after a failed database commit, or after a successful switch.
func (p *Prepared) Abort() {
	m := p.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if p.finished {
		return
	}
	p.finished = true
	for _, g := range p.created {
		g.listener.Close()
	}
}

// Commit switches acceptance atomically, then closes only old listening sockets.
// Accepted connections retain their original handler and crypto configuration.
func (p *Prepared) Commit() error {
	m := p.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if p.finished || m.closed || m.ctx.Err() != nil || p.base != m.version {
		if !p.finished {
			p.finished = true
			for _, g := range p.created {
				g.listener.Close()
			}
		}
		return ErrStale
	}
	p.finished = true
	old := m.active
	for name, g := range p.next {
		g.spec = p.specs[name]
	}
	m.active = p.next
	m.version++
	for _, g := range p.created {
		m.wg.Add(1)
		go m.accept(g)
	}
	for name, g := range old {
		if p.next[name] != g {
			g.listener.Close()
		}
	}
	return nil
}

func (m *Manager) accept(g *generation) {
	defer m.wg.Done()
	for {
		conn, err := g.listener.Accept()
		if err != nil {
			return
		}
		m.mu.Lock()
		if m.closed || m.active[g.spec.Name] != g {
			m.mu.Unlock()
			conn.Close()
			continue
		}
		select {
		case m.slots <- struct{}{}:
		default:
			m.mu.Unlock()
			conn.Close()
			continue
		}
		handler := g.spec.Handle
		m.connections[conn] = true
		m.wg.Add(1)
		m.mu.Unlock()
		go func() {
			defer m.wg.Done()
			defer func() { conn.Close(); m.mu.Lock(); delete(m.connections, conn); m.mu.Unlock(); <-m.slots }()
			handler(m.ctx, conn)
		}()
	}
}

func (m *Manager) States() []State {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []State{}
	for name, g := range m.active {
		out = append(out, State{Name: name, Address: g.listener.Addr().String(), Identity: g.spec.Identity})
	}
	return out
}

// Close stops acceptance and interrupts all remaining connections, e.g. on
// application shutdown or restore quarantine. It does not wait on handlers.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.closed = true
	m.cancel()
	for _, g := range m.active {
		g.listener.Close()
	}
	m.active = map[string]*generation{}
	for c := range m.connections {
		c.Close()
	}
}
func (m *Manager) Wait() { m.wg.Wait() }
