package session

import (
	"sync"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// AddConfigObserver registers fn for every replacement of the live
// configuration and returns the function that removes it again.
//
// storeConfig is the single point every reload passes through - the settings
// screen's PUT, the agent's own config_commit tool, the console's reloader -
// so one observer here sees them all. That is what lets `coddy serve` restart
// a subsystem whose settings moved without anyone touching the process: an
// operator connected to a node through a relay changes the bot token and the
// gateway comes back on the new one.
//
// fn runs on the goroutine that replaced the configuration and MUST NOT block:
// hand the value to a buffered channel and return. Observers are removed
// individually because several surfaces may watch one manager.
func (m *Manager) AddConfigObserver(fn func(*config.Config)) (remove func()) {
	if fn == nil {
		return func() {}
	}
	m.cfgObserverMu.Lock()
	if m.cfgObservers == nil {
		m.cfgObservers = make(map[int]func(*config.Config))
	}
	m.cfgObserverSeq++
	id := m.cfgObserverSeq
	m.cfgObservers[id] = fn
	m.cfgObserverMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			m.cfgObserverMu.Lock()
			delete(m.cfgObservers, id)
			m.cfgObserverMu.Unlock()
		})
	}
}

// publishConfigReplaced fans the new configuration out to the observers.
func (m *Manager) publishConfigReplaced(next *config.Config) {
	if next == nil {
		return
	}
	m.cfgObserverMu.Lock()
	fns := make([]func(*config.Config), 0, len(m.cfgObservers))
	for _, fn := range m.cfgObservers {
		fns = append(fns, fn)
	}
	m.cfgObserverMu.Unlock()
	for _, fn := range fns {
		fn(next)
	}
}
