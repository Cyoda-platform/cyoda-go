package memory

import (
	"context"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

type StateMachineAuditStore struct {
	tenant  spi.TenantID
	factory *StoreFactory
}

func (s *StateMachineAuditStore) Record(ctx context.Context, entityID string, event spi.StateMachineEvent) error {
	s.factory.smAuditMu.Lock()
	defer s.factory.smAuditMu.Unlock()
	if s.factory.smAudit[s.tenant] == nil {
		s.factory.smAudit[s.tenant] = make(map[string][]spi.StateMachineEvent)
	}
	cp := copyEvent(event)
	s.factory.smAudit[s.tenant][entityID] = append(s.factory.smAudit[s.tenant][entityID], cp)
	return nil
}

func (s *StateMachineAuditStore) GetEvents(ctx context.Context, entityID string) ([]spi.StateMachineEvent, error) {
	s.factory.smAuditMu.RLock()
	defer s.factory.smAuditMu.RUnlock()
	tenantData, ok := s.factory.smAudit[s.tenant]
	if !ok {
		return []spi.StateMachineEvent{}, nil
	}
	events, ok := tenantData[entityID]
	if !ok {
		return []spi.StateMachineEvent{}, nil
	}
	return copyEvents(events), nil
}

func (s *StateMachineAuditStore) GetEventsByTransaction(ctx context.Context, entityID string, transactionID string) ([]spi.StateMachineEvent, error) {
	s.factory.smAuditMu.RLock()
	defer s.factory.smAuditMu.RUnlock()
	tenantData, ok := s.factory.smAudit[s.tenant]
	if !ok {
		return []spi.StateMachineEvent{}, nil
	}
	events, ok := tenantData[entityID]
	if !ok {
		return []spi.StateMachineEvent{}, nil
	}
	filtered := []spi.StateMachineEvent{}
	for _, e := range events {
		if e.TransactionID == transactionID {
			filtered = append(filtered, copyEvent(e))
		}
	}
	return filtered, nil
}

func copyEvent(e spi.StateMachineEvent) spi.StateMachineEvent {
	cp := e
	if e.Data != nil {
		cp.Data = make(map[string]any, len(e.Data))
		for k, v := range e.Data {
			cp.Data[k] = v
		}
	}
	return cp
}

func copyEvents(events []spi.StateMachineEvent) []spi.StateMachineEvent {
	out := make([]spi.StateMachineEvent, len(events))
	for i, e := range events {
		out[i] = copyEvent(e)
	}
	return out
}
