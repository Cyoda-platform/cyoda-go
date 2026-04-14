package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"time"

	"github.com/jackc/pgx/v5"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// entityStore implements spi.EntityStore backed by PostgreSQL with
// dual-table writes: entities (current state) + entity_versions (history).
type entityStore struct {
	q        Querier
	tenantID spi.TenantID
}

func (s *entityStore) SaveAll(ctx context.Context, entities iter.Seq[*spi.Entity]) ([]int64, error) {
	return spi.DefaultSaveAll(s, ctx, entities)
}

func (s *entityStore) Save(ctx context.Context, entity *spi.Entity) (int64, error) {
	// Defensive copy — stores own their copies (Ownership Rule 4).
	e := *entity
	if entity.Data != nil {
		e.Data = make([]byte, len(entity.Data))
		copy(e.Data, entity.Data)
	}
	entity = &e

	tid := string(s.tenantID)
	eid := entity.Meta.ID

	entity.Meta.TenantID = s.tenantID

	// Determine next version.
	var maxVersion int64
	err := s.q.QueryRow(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM entity_versions WHERE tenant_id = $1 AND entity_id = $2`,
		tid, eid).Scan(&maxVersion)
	if err != nil {
		return 0, fmt.Errorf("failed to get max version for entity %s: %w", eid, err)
	}
	nextVersion := maxVersion + 1
	entity.Meta.Version = nextVersion

	// Get DB timestamps: CURRENT_TIMESTAMP (stable within tx) for valid_time/transaction_time,
	// clock_timestamp() (actual wall clock) for wall_clock_time.
	var dbNow, wallClockTime time.Time
	if err := s.q.QueryRow(ctx, `SELECT CURRENT_TIMESTAMP, clock_timestamp()`).Scan(&dbNow, &wallClockTime); err != nil {
		return 0, fmt.Errorf("failed to get DB timestamps: %w", err)
	}

	// Set metadata based on version.
	if nextVersion == 1 {
		entity.Meta.ChangeType = "CREATED"
		entity.Meta.CreationDate = dbNow
	} else {
		if entity.Meta.ChangeType == "" || entity.Meta.ChangeType == "CREATED" {
			entity.Meta.ChangeType = "UPDATED"
		}
	}
	entity.Meta.LastModifiedDate = dbNow

	// Marshal document.
	doc, err := marshalEntityDoc(entity, dbNow, dbNow, wallClockTime, false)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal entity doc: %w", err)
	}

	// Insert version row (explicit wall_clock_time to match _meta value).
	_, err = s.q.Exec(ctx,
		`INSERT INTO entity_versions (tenant_id, entity_id, model_name, model_version, version, valid_time, wall_clock_time, doc)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		tid, eid,
		entity.Meta.ModelRef.EntityName, entity.Meta.ModelRef.ModelVersion,
		nextVersion, dbNow, wallClockTime, doc)
	if err != nil {
		return 0, fmt.Errorf("failed to insert entity version: %w", err)
	}

	// Upsert current state row.
	_, err = s.q.Exec(ctx,
		`INSERT INTO entities (tenant_id, entity_id, model_name, model_version, version, deleted, doc)
		 VALUES ($1, $2, $3, $4, $5, false, $6)
		 ON CONFLICT (tenant_id, entity_id) DO UPDATE SET
		   model_name = EXCLUDED.model_name,
		   model_version = EXCLUDED.model_version,
		   version = EXCLUDED.version,
		   deleted = false,
		   doc = EXCLUDED.doc`,
		tid, eid,
		entity.Meta.ModelRef.EntityName, entity.Meta.ModelRef.ModelVersion,
		nextVersion, doc)
	if err != nil {
		return 0, fmt.Errorf("failed to upsert entity: %w", err)
	}

	return nextVersion, nil
}

func (s *entityStore) CompareAndSave(ctx context.Context, entity *spi.Entity, expectedTxID string) (int64, error) {
	tid := string(s.tenantID)
	eid := entity.Meta.ID

	// Check current transaction ID.
	var currentTxID *string
	err := s.q.QueryRow(ctx,
		`SELECT doc->'_meta'->>'transaction_id' FROM entities WHERE tenant_id = $1 AND entity_id = $2`,
		tid, eid).Scan(&currentTxID)
	if err != nil && err != pgx.ErrNoRows {
		return 0, fmt.Errorf("failed to check transaction ID: %w", err)
	}

	// If entity exists and txID doesn't match, conflict.
	if err == nil && currentTxID != nil && *currentTxID != expectedTxID {
		return 0, fmt.Errorf("entity %s transaction ID mismatch (current=%q, expected=%q): %w",
			eid, *currentTxID, expectedTxID, spi.ErrConflict)
	}

	return s.Save(ctx, entity)
}

func (s *entityStore) Get(ctx context.Context, entityID string) (*spi.Entity, error) {
	var doc []byte
	err := s.q.QueryRow(ctx,
		`SELECT doc FROM entities WHERE tenant_id = $1 AND entity_id = $2 AND NOT deleted`,
		string(s.tenantID), entityID).Scan(&doc)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("ENTITY_NOT_FOUND: entity %s not found: %w", entityID, spi.ErrNotFound)
		}
		return nil, fmt.Errorf("failed to get entity %s: %w", entityID, err)
	}
	return unmarshalEntityDoc(doc)
}

func (s *entityStore) GetAsAt(ctx context.Context, entityID string, asAt time.Time) (*spi.Entity, error) {
	// Round up to the next millisecond boundary (matching memory implementation).
	asAt = asAt.Truncate(time.Millisecond).Add(time.Millisecond)

	var doc []byte
	err := s.q.QueryRow(ctx,
		`SELECT doc FROM entity_versions
		 WHERE tenant_id = $1 AND entity_id = $2
		   AND valid_time <= $3
		   AND transaction_time <= CURRENT_TIMESTAMP
		 ORDER BY valid_time DESC, transaction_time DESC
		 LIMIT 1`,
		string(s.tenantID), entityID, asAt).Scan(&doc)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("ENTITY_NOT_FOUND: entity %s not found at %v: %w", entityID, asAt, spi.ErrNotFound)
		}
		return nil, fmt.Errorf("failed to get entity %s as-at %v: %w", entityID, asAt, err)
	}

	// Check if the version is deleted.
	var docMap map[string]json.RawMessage
	if err := json.Unmarshal(doc, &docMap); err != nil {
		return nil, fmt.Errorf("failed to parse entity doc: %w", err)
	}
	if metaRaw, ok := docMap["_meta"]; ok {
		var meta entityMeta
		if err := json.Unmarshal(metaRaw, &meta); err != nil {
			return nil, fmt.Errorf("failed to parse _meta: %w", err)
		}
		if meta.Deleted {
			return nil, fmt.Errorf("ENTITY_NOT_FOUND: entity %s deleted at %v: %w", entityID, asAt, spi.ErrNotFound)
		}
	}

	return unmarshalEntityDoc(doc)
}

func (s *entityStore) GetAll(ctx context.Context, modelRef spi.ModelRef) ([]*spi.Entity, error) {
	rows, err := s.q.Query(ctx,
		`SELECT doc FROM entities WHERE tenant_id = $1 AND model_name = $2 AND model_version = $3 AND NOT deleted`,
		string(s.tenantID), modelRef.EntityName, modelRef.ModelVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to query entities: %w", err)
	}
	defer rows.Close()

	return scanEntities(rows)
}

func (s *entityStore) GetAllAsAt(ctx context.Context, modelRef spi.ModelRef, asAt time.Time) ([]*spi.Entity, error) {
	asAt = asAt.Truncate(time.Millisecond).Add(time.Millisecond)

	rows, err := s.q.Query(ctx,
		`SELECT v.doc
		 FROM entities e
		 CROSS JOIN LATERAL (
		     SELECT doc FROM entity_versions ev
		     WHERE ev.tenant_id = e.tenant_id AND ev.entity_id = e.entity_id
		       AND ev.valid_time <= $4
		       AND ev.transaction_time <= CURRENT_TIMESTAMP
		     ORDER BY ev.valid_time DESC, ev.transaction_time DESC
		     LIMIT 1
		 ) v
		 WHERE e.tenant_id = $1 AND e.model_name = $2 AND e.model_version = $3`,
		string(s.tenantID), modelRef.EntityName, modelRef.ModelVersion, asAt)
	if err != nil {
		return nil, fmt.Errorf("failed to query entities as-at: %w", err)
	}
	defer rows.Close()

	return scanEntitiesFilterDeleted(rows)
}

func (s *entityStore) Delete(ctx context.Context, entityID string) error {
	tid := string(s.tenantID)

	// Get current entity to verify it exists and get model info.
	var doc []byte
	err := s.q.QueryRow(ctx,
		`SELECT doc FROM entities WHERE tenant_id = $1 AND entity_id = $2 AND NOT deleted`,
		tid, entityID).Scan(&doc)
	if err != nil {
		if err == pgx.ErrNoRows {
			return fmt.Errorf("ENTITY_NOT_FOUND: entity %s not found: %w", entityID, spi.ErrNotFound)
		}
		return fmt.Errorf("failed to get entity %s for delete: %w", entityID, err)
	}

	current, err := unmarshalEntityDoc(doc)
	if err != nil {
		return fmt.Errorf("failed to unmarshal entity for delete: %w", err)
	}

	// Determine next version.
	var maxVersion int64
	err = s.q.QueryRow(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM entity_versions WHERE tenant_id = $1 AND entity_id = $2`,
		tid, entityID).Scan(&maxVersion)
	if err != nil {
		return fmt.Errorf("failed to get max version for delete: %w", err)
	}
	nextVersion := maxVersion + 1

	// Get DB timestamp.
	var dbNow, wallClockTime time.Time
	if err := s.q.QueryRow(ctx, `SELECT CURRENT_TIMESTAMP, clock_timestamp()`).Scan(&dbNow, &wallClockTime); err != nil {
		return fmt.Errorf("failed to get DB timestamps: %w", err)
	}

	// Prepare delete entity.
	current.Meta.Version = nextVersion
	current.Meta.ChangeType = "DELETED"
	current.Meta.LastModifiedDate = dbNow

	deleteDoc, err := marshalEntityDoc(current, dbNow, dbNow, wallClockTime, true)
	if err != nil {
		return fmt.Errorf("failed to marshal delete doc: %w", err)
	}

	// Insert delete version.
	_, err = s.q.Exec(ctx,
		`INSERT INTO entity_versions (tenant_id, entity_id, model_name, model_version, version, valid_time, wall_clock_time, doc)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		tid, entityID,
		current.Meta.ModelRef.EntityName, current.Meta.ModelRef.ModelVersion,
		nextVersion, dbNow, wallClockTime, deleteDoc)
	if err != nil {
		return fmt.Errorf("failed to insert delete version: %w", err)
	}

	// Update entities table to mark deleted.
	_, err = s.q.Exec(ctx,
		`UPDATE entities SET version = $1, deleted = true, doc = $2 WHERE tenant_id = $3 AND entity_id = $4`,
		nextVersion, deleteDoc, tid, entityID)
	if err != nil {
		return fmt.Errorf("failed to mark entity deleted: %w", err)
	}

	return nil
}

func (s *entityStore) DeleteAll(ctx context.Context, modelRef spi.ModelRef) error {
	tid := string(s.tenantID)

	// Get all entity IDs for this model.
	rows, err := s.q.Query(ctx,
		`SELECT entity_id FROM entities WHERE tenant_id = $1 AND model_name = $2 AND model_version = $3 AND NOT deleted`,
		tid, modelRef.EntityName, modelRef.ModelVersion)
	if err != nil {
		return fmt.Errorf("failed to query entities for deleteAll: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("failed to scan entity ID: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("row iteration error: %w", err)
	}

	for _, id := range ids {
		if err := s.Delete(ctx, id); err != nil {
			return fmt.Errorf("failed to delete entity %s: %w", id, err)
		}
	}

	return nil
}

func (s *entityStore) Exists(ctx context.Context, entityID string) (bool, error) {
	var exists bool
	err := s.q.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM entities WHERE tenant_id = $1 AND entity_id = $2 AND NOT deleted)`,
		string(s.tenantID), entityID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check existence of entity %s: %w", entityID, err)
	}
	return exists, nil
}

func (s *entityStore) Count(ctx context.Context, modelRef spi.ModelRef) (int64, error) {
	var count int64
	err := s.q.QueryRow(ctx,
		`SELECT count(*) FROM entities WHERE tenant_id = $1 AND model_name = $2 AND model_version = $3 AND NOT deleted`,
		string(s.tenantID), modelRef.EntityName, modelRef.ModelVersion).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count entities: %w", err)
	}
	return count, nil
}

func (s *entityStore) GetVersionHistory(ctx context.Context, entityID string) ([]spi.EntityVersion, error) {
	rows, err := s.q.Query(ctx,
		`SELECT doc, version, valid_time FROM entity_versions
		 WHERE tenant_id = $1 AND entity_id = $2
		 ORDER BY version ASC`,
		string(s.tenantID), entityID)
	if err != nil {
		return nil, fmt.Errorf("failed to query version history: %w", err)
	}
	defer rows.Close()

	var history []spi.EntityVersion
	for rows.Next() {
		var doc []byte
		var version int64
		var validTime time.Time
		if err := rows.Scan(&doc, &version, &validTime); err != nil {
			return nil, fmt.Errorf("failed to scan version row: %w", err)
		}
		ver, err := unmarshalEntityVersion(doc, version, validTime)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal version %d: %w", version, err)
		}
		history = append(history, *ver)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return history, nil
}

// scanEntities reads all Entity rows from a result set.
func scanEntities(rows pgx.Rows) ([]*spi.Entity, error) {
	var result []*spi.Entity
	for rows.Next() {
		var doc []byte
		if err := rows.Scan(&doc); err != nil {
			return nil, fmt.Errorf("failed to scan entity row: %w", err)
		}
		ent, err := unmarshalEntityDoc(doc)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal entity: %w", err)
		}
		result = append(result, ent)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}
	if result == nil {
		result = []*spi.Entity{}
	}
	return result, nil
}

// scanEntitiesFilterDeleted reads Entity rows and filters out deleted ones.
func scanEntitiesFilterDeleted(rows pgx.Rows) ([]*spi.Entity, error) {
	var result []*spi.Entity
	for rows.Next() {
		var doc []byte
		if err := rows.Scan(&doc); err != nil {
			return nil, fmt.Errorf("failed to scan entity row: %w", err)
		}

		// Check deleted flag in _meta.
		var docMap map[string]json.RawMessage
		if err := json.Unmarshal(doc, &docMap); err != nil {
			return nil, fmt.Errorf("failed to parse entity doc: %w", err)
		}
		if metaRaw, ok := docMap["_meta"]; ok {
			var meta entityMeta
			if err := json.Unmarshal(metaRaw, &meta); err != nil {
				return nil, fmt.Errorf("failed to parse _meta: %w", err)
			}
			if meta.Deleted {
				continue
			}
		}

		ent, err := unmarshalEntityDoc(doc)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal entity: %w", err)
		}
		result = append(result, ent)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}
	if result == nil {
		result = []*spi.Entity{}
	}
	return result, nil
}
