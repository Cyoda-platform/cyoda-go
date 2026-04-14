package memory

import (
	"context"
	"fmt"

	"github.com/cyoda-platform/cyoda-go/internal/common"
)

type StateMachineAuditStore struct {
	tenant  common.TenantID
	factory *StoreFactory
}

func (s *StateMachineAuditStore) Record(ctx context.Context, entityID string, event common.StateMachineEvent) error {
	s.factory.smAuditMu.Lock()
	defer s.factory.smAuditMu.Unlock()
	if s.factory.smAudit[s.tenant] == nil {
		s.factory.smAudit[s.tenant] = make(map[string][]common.StateMachineEvent)
	}
	cp := copyEvent(event)
	s.factory.smAudit[s.tenant][entityID] = append(s.factory.smAudit[s.tenant][entityID], cp)
	return nil
}

func (s *StateMachineAuditStore) GetEvents(ctx context.Context, entityID string) ([]common.StateMachineEvent, error) {
	s.factory.smAuditMu.RLock()
	defer s.factory.smAuditMu.RUnlock()
	tenantData, ok := s.factory.smAudit[s.tenant]
	if !ok {
		return nil, fmt.Errorf("no events found for entity %s", entityID)
	}
	events, ok := tenantData[entityID]
	if !ok {
		return nil, fmt.Errorf("no events found for entity %s", entityID)
	}
	return copyEvents(events), nil
}

func (s *StateMachineAuditStore) GetEventsByTransaction(ctx context.Context, entityID string, transactionID string) ([]common.StateMachineEvent, error) {
	s.factory.smAuditMu.RLock()
	defer s.factory.smAuditMu.RUnlock()
	tenantData, ok := s.factory.smAudit[s.tenant]
	if !ok {
		return nil, fmt.Errorf("no events found for entity %s", entityID)
	}
	events, ok := tenantData[entityID]
	if !ok {
		return nil, fmt.Errorf("no events found for entity %s", entityID)
	}
	var filtered []common.StateMachineEvent
	for _, e := range events {
		if e.TransactionID == transactionID {
			filtered = append(filtered, copyEvent(e))
		}
	}
	return filtered, nil
}

func copyEvent(e common.StateMachineEvent) common.StateMachineEvent {
	cp := e
	if e.Data != nil {
		cp.Data = make(map[string]any, len(e.Data))
		for k, v := range e.Data {
			cp.Data[k] = v
		}
	}
	return cp
}

func copyEvents(events []common.StateMachineEvent) []common.StateMachineEvent {
	out := make([]common.StateMachineEvent, len(events))
	for i, e := range events {
		out[i] = copyEvent(e)
	}
	return out
}
