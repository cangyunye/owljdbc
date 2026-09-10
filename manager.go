package owljdbc

import (
	"context"
	"strings"
	"sync"
)

// Manager 按 profile（classpath 指纹）复用 agent 进程。
type Manager struct {
	mu    sync.Mutex
	procs map[string]*procEntry
}

type procEntry struct {
	proc *AgentProc
	refs int
}

var DefaultManager = &Manager{procs: make(map[string]*procEntry)}

func (m *Manager) Acquire(ctx context.Context, cfg Config) (*Session, error) {
	key := profileKey(cfg)
	m.mu.Lock()
	e := m.procs[key]
	if e == nil {
		p, err := openAgent(ctx, cfg)
		if err != nil {
			m.mu.Unlock()
			return nil, err
		}
		e = &procEntry{proc: p}
		m.procs[key] = e
	}
	e.refs++
	m.mu.Unlock()

	s, err := e.proc.NewSession(cfg)
	if err != nil {
		m.Release(cfg)
		return nil, err
	}
	return s, nil
}

func (m *Manager) Release(cfg Config) {
	key := profileKey(cfg)
	m.mu.Lock()
	e := m.procs[key]
	if e != nil {
		e.refs--
		if e.refs <= 0 {
			delete(m.procs, key)
			m.mu.Unlock()
			e.proc.close()
			return
		}
	}
	m.mu.Unlock()
}

func profileKey(cfg Config) string {
	return strings.Join(cfg.Classpath, "|") + "|" + cfg.JavaHome + "|" + cfg.AgentJar
}
