package lifecycle

import (
	"context"
	"sync"
	"time"
)

type Outcome int

const (
	OutcomeCommitted  Outcome = 1
	OutcomeRolledBack Outcome = 2
)

type txEntry struct {
	NodeID    string
	ExpiresAt time.Time
}

type outcomeEntry struct {
	Outcome    Outcome
	RecordedAt time.Time
}

type Manager struct {
	mu         sync.RWMutex
	active     map[string]txEntry
	outcomes   map[string]outcomeEntry
	outcomeTTL time.Duration
}

// NewManager creates a new lifecycle Manager. The outcomeTTL parameter controls
// how long completed transaction outcomes are retained before being cleaned up.
func NewManager(outcomeTTL time.Duration) *Manager {
	return &Manager{
		active:     make(map[string]txEntry),
		outcomes:   make(map[string]outcomeEntry),
		outcomeTTL: outcomeTTL,
	}
}

func (m *Manager) Register(_ context.Context, txID string, nodeID string, ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.active[txID] = txEntry{NodeID: nodeID, ExpiresAt: time.Now().Add(ttl)}
}

func (m *Manager) IsAlive(_ context.Context, txID string) (bool, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.active[txID]
	if !ok {
		return false, "", nil
	}
	if time.Now().After(entry.ExpiresAt) {
		return false, "", nil
	}
	return true, entry.NodeID, nil
}

func (m *Manager) RecordOutcome(_ context.Context, txID string, outcome Outcome) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.active, txID)
	m.outcomes[txID] = outcomeEntry{Outcome: outcome, RecordedAt: time.Now()}
}

func (m *Manager) GetOutcome(_ context.Context, txID string) (Outcome, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.outcomes[txID]
	return entry.Outcome, ok
}

func (m *Manager) ReapExpired(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	reaped := 0
	for txID, entry := range m.active {
		if now.After(entry.ExpiresAt) {
			delete(m.active, txID)
			m.outcomes[txID] = outcomeEntry{Outcome: OutcomeRolledBack, RecordedAt: now}
			reaped++
		}
	}
	// Clean up old outcome entries that have exceeded their TTL.
	for txID, entry := range m.outcomes {
		if now.Sub(entry.RecordedAt) > m.outcomeTTL {
			delete(m.outcomes, txID)
		}
	}
	return reaped, nil
}

func (m *Manager) ListByNode(_ context.Context, nodeID string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var txIDs []string
	now := time.Now()
	for txID, entry := range m.active {
		if entry.NodeID == nodeID && now.Before(entry.ExpiresAt) {
			txIDs = append(txIDs, txID)
		}
	}
	return txIDs
}

func (m *Manager) Remove(_ context.Context, txID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.active, txID)
}
