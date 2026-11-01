package update

// setState replaces the manager's state outright. Only tests need it, to
// stand in for a fresh process after a restart.
func (m *Manager) setState(s State, cb func(State)) {
	m.mu.Lock()
	m.state = s
	m.mu.Unlock()
	if cb != nil {
		cb(s)
	}
}
