package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cyoda-platform/cyoda-go/internal/common"
	"github.com/jackc/pgx/v5"
)

// smAuditStore implements spi.StateMachineAuditStore backed by PostgreSQL.
type smAuditStore struct {
	q        Querier
	tenantID common.TenantID
}

// Record appends an audit event for the given entity. It is append-only;
// no upsert is performed. event.TimeUUID is used as the event_id primary key.
func (s *smAuditStore) Record(ctx context.Context, entityID string, event common.StateMachineEvent) error {
	doc, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal state machine event: %w", err)
	}

	_, err = s.q.Exec(ctx,
		`INSERT INTO sm_audit_events (tenant_id, entity_id, event_id, transaction_id, timestamp, doc)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		string(s.tenantID), entityID, event.TimeUUID, event.TransactionID, event.Timestamp, doc)
	if err != nil {
		return fmt.Errorf("failed to record state machine event %s for entity %s: %w", event.TimeUUID, entityID, err)
	}
	return nil
}

// GetEvents returns all audit events for the given entity, ordered by
// timestamp ascending. Returns an error when no events exist for the entity.
func (s *smAuditStore) GetEvents(ctx context.Context, entityID string) ([]common.StateMachineEvent, error) {
	rows, err := s.q.Query(ctx,
		`SELECT doc FROM sm_audit_events
		 WHERE tenant_id = $1 AND entity_id = $2
		 ORDER BY timestamp ASC`,
		string(s.tenantID), entityID)
	if err != nil {
		return nil, fmt.Errorf("failed to query events for entity %s: %w", entityID, err)
	}
	defer rows.Close()

	events, err := scanEventRows(rows)
	if err != nil {
		return nil, fmt.Errorf("failed to scan events for entity %s: %w", entityID, err)
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("no events found for entity %s", entityID)
	}
	return events, nil
}

// GetEventsByTransaction returns audit events for the given entity that belong
// to the specified transaction, ordered by timestamp ascending.
// Unlike GetEvents, it returns an empty slice (not an error) when no events
// match the transaction.
func (s *smAuditStore) GetEventsByTransaction(ctx context.Context, entityID string, transactionID string) ([]common.StateMachineEvent, error) {
	rows, err := s.q.Query(ctx,
		`SELECT doc FROM sm_audit_events
		 WHERE tenant_id = $1 AND entity_id = $2 AND transaction_id = $3
		 ORDER BY timestamp ASC`,
		string(s.tenantID), entityID, transactionID)
	if err != nil {
		return nil, fmt.Errorf("failed to query events for entity %s, transaction %s: %w", entityID, transactionID, err)
	}
	defer rows.Close()

	events, err := scanEventRows(rows)
	if err != nil {
		return nil, fmt.Errorf("failed to scan events for entity %s, transaction %s: %w", entityID, transactionID, err)
	}
	return events, nil
}

// scanEventRows reads all rows from a doc JSONB query and unmarshals each into
// a StateMachineEvent. The caller is responsible for closing rows.
func scanEventRows(rows pgx.Rows) ([]common.StateMachineEvent, error) {
	var events []common.StateMachineEvent
	for rows.Next() {
		var doc []byte
		if err := rows.Scan(&doc); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		var e common.StateMachineEvent
		if err := json.Unmarshal(doc, &e); err != nil {
			return nil, fmt.Errorf("failed to unmarshal event doc: %w", err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}
	return events, nil
}
