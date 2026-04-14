package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cyoda-platform/cyoda-go/internal/common"
	"github.com/jackc/pgx/v5"
)

// modelStore implements spi.ModelStore backed by PostgreSQL.
type modelStore struct {
	q        Querier
	tenantID common.TenantID
}

// modelDoc is the JSON representation stored in the doc JSONB column.
// Field names match the spec: camelCase, ref nested.
type modelDoc struct {
	Ref struct {
		EntityName   string `json:"entityName"`
		ModelVersion string `json:"modelVersion"`
	} `json:"ref"`
	State       common.ModelState  `json:"state"`
	ChangeLevel common.ChangeLevel `json:"changeLevel"`
	UpdateDate  string             `json:"updateDate"` // RFC3339Nano
	Schema      []byte             `json:"schema"`
}

func (s *modelStore) Save(ctx context.Context, desc *common.ModelDescriptor) error {
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

	_, err = s.q.Exec(ctx,
		`INSERT INTO models (tenant_id, model_name, model_version, doc)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (tenant_id, model_name, model_version) DO UPDATE SET doc = EXCLUDED.doc`,
		string(s.tenantID), desc.Ref.EntityName, desc.Ref.ModelVersion, raw)
	if err != nil {
		return fmt.Errorf("failed to save model %s: %w", desc.Ref, err)
	}
	return nil
}

func (s *modelStore) Get(ctx context.Context, modelRef common.ModelRef) (*common.ModelDescriptor, error) {
	var raw []byte
	err := s.q.QueryRow(ctx,
		`SELECT doc FROM models WHERE tenant_id = $1 AND model_name = $2 AND model_version = $3`,
		string(s.tenantID), modelRef.EntityName, modelRef.ModelVersion).Scan(&raw)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("model %s not found: %w", modelRef, common.ErrNotFound)
		}
		return nil, fmt.Errorf("failed to get model %s: %w", modelRef, err)
	}
	return unmarshalModelDoc(raw)
}

func (s *modelStore) GetAll(ctx context.Context) ([]common.ModelRef, error) {
	rows, err := s.q.Query(ctx,
		`SELECT model_name, model_version FROM models WHERE tenant_id = $1`,
		string(s.tenantID))
	if err != nil {
		return nil, fmt.Errorf("failed to list models: %w", err)
	}
	defer rows.Close()

	refs := make([]common.ModelRef, 0)
	for rows.Next() {
		var name, version string
		if err := rows.Scan(&name, &version); err != nil {
			return nil, fmt.Errorf("failed to scan model row: %w", err)
		}
		refs = append(refs, common.ModelRef{EntityName: name, ModelVersion: version})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}
	return refs, nil
}

func (s *modelStore) Delete(ctx context.Context, modelRef common.ModelRef) error {
	_, err := s.q.Exec(ctx,
		`DELETE FROM models WHERE tenant_id = $1 AND model_name = $2 AND model_version = $3`,
		string(s.tenantID), modelRef.EntityName, modelRef.ModelVersion)
	if err != nil {
		return fmt.Errorf("failed to delete model %s: %w", modelRef, err)
	}
	return nil
}

func (s *modelStore) Lock(ctx context.Context, modelRef common.ModelRef) error {
	return s.updateStateField(ctx, modelRef, common.ModelLocked, "lock")
}

func (s *modelStore) Unlock(ctx context.Context, modelRef common.ModelRef) error {
	return s.updateStateField(ctx, modelRef, common.ModelUnlocked, "unlock")
}

func (s *modelStore) IsLocked(ctx context.Context, modelRef common.ModelRef) (bool, error) {
	var raw []byte
	err := s.q.QueryRow(ctx,
		`SELECT doc FROM models WHERE tenant_id = $1 AND model_name = $2 AND model_version = $3`,
		string(s.tenantID), modelRef.EntityName, modelRef.ModelVersion).Scan(&raw)
	if err != nil {
		if err == pgx.ErrNoRows {
			return false, fmt.Errorf("model %s not found: %w", modelRef, common.ErrNotFound)
		}
		return false, fmt.Errorf("failed to check lock status for model %s: %w", modelRef, err)
	}

	desc, err := unmarshalModelDoc(raw)
	if err != nil {
		return false, err
	}
	return desc.State == common.ModelLocked, nil
}

func (s *modelStore) SetChangeLevel(ctx context.Context, modelRef common.ModelRef, level common.ChangeLevel) error {
	tag, err := s.q.Exec(ctx,
		`UPDATE models
		 SET doc = jsonb_set(doc, '{changeLevel}', to_jsonb($4::text))
		 WHERE tenant_id = $1 AND model_name = $2 AND model_version = $3`,
		string(s.tenantID), modelRef.EntityName, modelRef.ModelVersion, string(level))
	if err != nil {
		return fmt.Errorf("failed to set change level for model %s: %w", modelRef, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("model %s not found: %w", modelRef, common.ErrNotFound)
	}
	return nil
}

// updateStateField updates the state field in the doc JSONB column for Lock/Unlock.
func (s *modelStore) updateStateField(ctx context.Context, modelRef common.ModelRef, state common.ModelState, op string) error {
	tag, err := s.q.Exec(ctx,
		`UPDATE models
		 SET doc = jsonb_set(doc, '{state}', to_jsonb($4::text))
		 WHERE tenant_id = $1 AND model_name = $2 AND model_version = $3`,
		string(s.tenantID), modelRef.EntityName, modelRef.ModelVersion, string(state))
	if err != nil {
		return fmt.Errorf("failed to %s model %s: %w", op, modelRef, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("model %s not found: %w", modelRef, common.ErrNotFound)
	}
	return nil
}

// unmarshalModelDoc deserializes the JSONB doc column into a ModelDescriptor.
func unmarshalModelDoc(raw []byte) (*common.ModelDescriptor, error) {
	var doc modelDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("failed to unmarshal model doc: %w", err)
	}

	updateDate, err := time.Parse(time.RFC3339Nano, doc.UpdateDate)
	if err != nil {
		return nil, fmt.Errorf("failed to parse update date %q: %w", doc.UpdateDate, err)
	}

	return &common.ModelDescriptor{
		Ref: common.ModelRef{
			EntityName:   doc.Ref.EntityName,
			ModelVersion: doc.Ref.ModelVersion,
		},
		State:       doc.State,
		ChangeLevel: doc.ChangeLevel,
		UpdateDate:  updateDate,
		Schema:      doc.Schema,
	}, nil
}
