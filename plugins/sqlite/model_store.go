package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

type modelStore struct {
	db       *sql.DB
	tenantID spi.TenantID
}

// modelDoc is the JSON representation stored in the doc BLOB column.
// Field names match the postgres plugin: camelCase, ref nested.
type modelDoc struct {
	Ref struct {
		EntityName   string `json:"entityName"`
		ModelVersion string `json:"modelVersion"`
	} `json:"ref"`
	State       spi.ModelState  `json:"state"`
	ChangeLevel spi.ChangeLevel `json:"changeLevel"`
	UpdateDate  string          `json:"updateDate"`
	Schema      []byte          `json:"schema"`
}

func (s *modelStore) Save(ctx context.Context, desc *spi.ModelDescriptor) error {
	var doc modelDoc
	doc.Ref.EntityName = desc.Ref.EntityName
	doc.Ref.ModelVersion = desc.Ref.ModelVersion
	doc.State = desc.State
	doc.ChangeLevel = desc.ChangeLevel
	doc.UpdateDate = desc.UpdateDate.UTC().Format(time.RFC3339Nano)
	doc.Schema = desc.Schema
	raw, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("failed to marshal model descriptor: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO models (tenant_id, model_name, model_version, doc)
		 VALUES (?, ?, ?, jsonb(?))`,
		string(s.tenantID), desc.Ref.EntityName, desc.Ref.ModelVersion, raw)
	if err != nil {
		return fmt.Errorf("failed to save model %s: %w", desc.Ref, classifyError(err))
	}
	return nil
}

func (s *modelStore) Get(ctx context.Context, modelRef spi.ModelRef) (*spi.ModelDescriptor, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT json(doc) FROM models WHERE tenant_id = ? AND model_name = ? AND model_version = ?`,
		string(s.tenantID), modelRef.EntityName, modelRef.ModelVersion).Scan(&raw)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("model %s not found: %w", modelRef, spi.ErrNotFound)
		}
		return nil, fmt.Errorf("failed to get model %s: %w", modelRef, classifyError(err))
	}
	return unmarshalModelDoc(raw)
}

func (s *modelStore) GetAll(ctx context.Context) ([]spi.ModelRef, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT model_name, model_version FROM models WHERE tenant_id = ?`,
		string(s.tenantID))
	if err != nil {
		return nil, fmt.Errorf("failed to list models: %w", err)
	}
	defer rows.Close()

	refs := make([]spi.ModelRef, 0)
	for rows.Next() {
		var name, version string
		if err := rows.Scan(&name, &version); err != nil {
			return nil, fmt.Errorf("failed to scan model row: %w", err)
		}
		refs = append(refs, spi.ModelRef{EntityName: name, ModelVersion: version})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}
	return refs, nil
}

func (s *modelStore) Delete(ctx context.Context, modelRef spi.ModelRef) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM models WHERE tenant_id = ? AND model_name = ? AND model_version = ?`,
		string(s.tenantID), modelRef.EntityName, modelRef.ModelVersion)
	if err != nil {
		return fmt.Errorf("failed to delete model %s: %w", modelRef, classifyError(err))
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("model %s not found: %w", modelRef, spi.ErrNotFound)
	}
	return nil
}

func (s *modelStore) Lock(ctx context.Context, modelRef spi.ModelRef) error {
	return s.updateStateField(ctx, modelRef, spi.ModelLocked, "lock")
}

func (s *modelStore) Unlock(ctx context.Context, modelRef spi.ModelRef) error {
	return s.updateStateField(ctx, modelRef, spi.ModelUnlocked, "unlock")
}

func (s *modelStore) IsLocked(ctx context.Context, modelRef spi.ModelRef) (bool, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT json(doc) FROM models WHERE tenant_id = ? AND model_name = ? AND model_version = ?`,
		string(s.tenantID), modelRef.EntityName, modelRef.ModelVersion).Scan(&raw)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, fmt.Errorf("model %s not found: %w", modelRef, spi.ErrNotFound)
		}
		return false, fmt.Errorf("failed to check lock status for model %s: %w", modelRef, err)
	}

	desc, err := unmarshalModelDoc(raw)
	if err != nil {
		return false, err
	}
	return desc.State == spi.ModelLocked, nil
}

func (s *modelStore) SetChangeLevel(ctx context.Context, modelRef spi.ModelRef, level spi.ChangeLevel) error {
	// SQLite has no jsonb_set; read-modify-write.
	var raw []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT json(doc) FROM models WHERE tenant_id = ? AND model_name = ? AND model_version = ?`,
		string(s.tenantID), modelRef.EntityName, modelRef.ModelVersion).Scan(&raw)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("model %s not found: %w", modelRef, spi.ErrNotFound)
		}
		return fmt.Errorf("failed to read model %s: %w", modelRef, err)
	}

	var doc modelDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("failed to unmarshal model doc: %w", err)
	}
	doc.ChangeLevel = level
	updated, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("failed to marshal model doc: %w", err)
	}

	res, err := s.db.ExecContext(ctx,
		`UPDATE models SET doc = jsonb(?) WHERE tenant_id = ? AND model_name = ? AND model_version = ?`,
		updated, string(s.tenantID), modelRef.EntityName, modelRef.ModelVersion)
	if err != nil {
		return fmt.Errorf("failed to set change level for model %s: %w", modelRef, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("model %s not found: %w", modelRef, spi.ErrNotFound)
	}
	return nil
}

// updateStateField reads the doc, sets the state, and writes it back.
// SQLite has no jsonb_set, so this is a read-modify-write.
func (s *modelStore) updateStateField(ctx context.Context, modelRef spi.ModelRef, state spi.ModelState, op string) error {
	var raw []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT json(doc) FROM models WHERE tenant_id = ? AND model_name = ? AND model_version = ?`,
		string(s.tenantID), modelRef.EntityName, modelRef.ModelVersion).Scan(&raw)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("model %s not found: %w", modelRef, spi.ErrNotFound)
		}
		return fmt.Errorf("failed to %s model %s: %w", op, modelRef, err)
	}

	var doc modelDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("failed to unmarshal model doc: %w", err)
	}
	doc.State = state
	updated, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("failed to marshal model doc: %w", err)
	}

	res, err := s.db.ExecContext(ctx,
		`UPDATE models SET doc = jsonb(?) WHERE tenant_id = ? AND model_name = ? AND model_version = ?`,
		updated, string(s.tenantID), modelRef.EntityName, modelRef.ModelVersion)
	if err != nil {
		return fmt.Errorf("failed to %s model %s: %w", op, modelRef, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("model %s not found: %w", modelRef, spi.ErrNotFound)
	}
	return nil
}

func unmarshalModelDoc(raw []byte) (*spi.ModelDescriptor, error) {
	var doc modelDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("failed to unmarshal model doc: %w", err)
	}

	updateDate, err := time.Parse(time.RFC3339Nano, doc.UpdateDate)
	if err != nil {
		return nil, fmt.Errorf("failed to parse update date %q: %w", doc.UpdateDate, err)
	}

	return &spi.ModelDescriptor{
		Ref: spi.ModelRef{
			EntityName:   doc.Ref.EntityName,
			ModelVersion: doc.Ref.ModelVersion,
		},
		State:       doc.State,
		ChangeLevel: doc.ChangeLevel,
		UpdateDate:  updateDate,
		Schema:      doc.Schema,
	}, nil
}

// ExtendSchema is a placeholder until Phase D (Task D3) implements
// the delta log semantics for the sqlite plugin.
func (s *modelStore) ExtendSchema(ctx context.Context, ref spi.ModelRef, delta spi.SchemaDelta) error {
	return fmt.Errorf("ExtendSchema not yet implemented")
}
