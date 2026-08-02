package bgtask

import "sync"

// The pool is process-wide on purpose. A background task outlives the tool call
// that started it and the whole turn around it, and both the agent loop and the
// HTTP surface have to see the same task list. Handing every one of those call
// sites its own pool would mean a command started from a turn is invisible to
// the drawer that is supposed to show it.
var (
	defaultOnce sync.Once
	defaultPool *Pool
)

// Default returns the process-wide pool, creating it with built-in defaults when
// the process never called Configure.
func Default() *Pool {
	defaultOnce.Do(func() {
		if defaultPool == nil {
			defaultPool = New(Config{})
		}
	})
	return defaultPool
}

// Configure applies operator configuration to the process-wide pool. Tasks
// already running keep the limits they started with; the new bounds apply to
// whatever starts next.
func Configure(cfg Config) {
	Default().SetConfig(cfg)
}

// SetConfig replaces the bounds applied to tasks started from now on.
func (p *Pool) SetConfig(cfg Config) {
	normalised := cfg.normalised()
	p.mu.Lock()
	p.cfg = normalised
	p.mu.Unlock()
}
