package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// modelStore implements spi.ModelStore backed by PostgreSQL.
type modelStore struct {
	q         Querier
	tenantID  spi.TenantID
	applyFunc ApplyFunc // optional; required when the extension log is non-empty
}

// modelDoc is the JSON representation stored in the doc JSONB column.
// Field names match the spec: camelCase, ref nested.
type modelDoc struct {
	Ref struct {
		EntityName   string `json:"entityName"`
		ModelVersion string `json:"modelVersion"`
	} `json:"ref"`
	State       spi.ModelState  `json:"state"`
	ChangeLevel spi.ChangeLevel `json:"changeLevel"`
	UpdateDate  string          `json:"updateDate"` // RFC3339Nano
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

	_, err = s.q.Exec(ctx,
		`INSERT INTO models (tenant_id, model_name, model_version, doc)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (tenant_id, model_name, model_version) DO UPDATE SET doc = EXCLUDED.doc`,
		string(s.tenantID), desc.Ref.EntityName, desc.Ref.ModelVersion, raw)
	if err != nil {
		return fmt.Errorf("failed to save model %s: %w", desc.Ref, err)
	}

	// Save is the full-replace lifecycle path. By the operator contract
	// (docs/CONSISTENCY.md) the extension log must already be empty for
	// this ref — ExtendSchema is disjoint from Save via the state
	// machine. Defensively DELETE: in dev builds a non-zero RowsAffected
	// here is a fatal assertion; in production it logs a warning and
	// proceeds, because Save itself is the authoritative schema source
	// at this moment.
	tag, err := s.q.Exec(ctx, `
		DELETE FROM model_schema_extensions
		WHERE tenant_id = $1 AND model_name = $2 AND model_version = $3`,
		string(s.tenantID), desc.Ref.EntityName, desc.Ref.ModelVersion)
	if err != nil {
		return fmt.Errorf("Save: clear extension log for %s: %w", desc.Ref, err)
	}
	if n := tag.RowsAffected(); n != 0 {
		if buildIsDev() {
			return fmt.Errorf("Save found %d stale extension rows for %s; operator-contract violation (see docs/CONSISTENCY.md)", n, desc.Ref)
		}
		slog.Warn("Save cleared stale extension rows",
			"pkg", "postgres", "ref", desc.Ref, "count", n)
	}
	return nil
}

func (s *modelStore) Get(ctx context.Context, modelRef spi.ModelRef) (*spi.ModelDescriptor, error) {
	var raw []byte
	err := s.q.QueryRow(ctx,
		`SELECT doc FROM models WHERE tenant_id = $1 AND model_name = $2 AND model_version = $3`,
		string(s.tenantID), modelRef.EntityName, modelRef.ModelVersion).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("model %s not found: %w", modelRef, spi.ErrNotFound)
		}
		return nil, fmt.Errorf("failed to get model %s: %w", modelRef, err)
	}
	desc, err := unmarshalModelDoc(raw)
	if err != nil {
		return nil, err
	}

	folded, err := s.foldLocked(ctx, modelRef, desc.Schema)
	if err != nil {
		return nil, fmt.Errorf("fold extension log for %s: %w", modelRef, err)
	}
	desc.Schema = folded
	return desc, nil
}

func (s *modelStore) GetAll(ctx context.Context) ([]spi.ModelRef, error) {
	rows, err := s.q.Query(ctx,
		`SELECT model_name, model_version FROM models WHERE tenant_id = $1`,
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
	tag, err := s.q.Exec(ctx,
		`DELETE FROM models WHERE tenant_id = $1 AND model_name = $2 AND model_version = $3`,
		string(s.tenantID), modelRef.EntityName, modelRef.ModelVersion)
	if err != nil {
		return fmt.Errorf("failed to delete model %s: %w", modelRef, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("model %s not found: %w", modelRef, spi.ErrNotFound)
	}
	return nil
}

func (s *modelStore) Lock(ctx context.Context, modelRef spi.ModelRef) error {
	return s.updateStateField(ctx, modelRef, spi.ModelLocked, "lock")
}

func (s *modelStore) Unlock(ctx context.Context, modelRef spi.ModelRef) error {
	if err := s.updateStateField(ctx, modelRef, spi.ModelUnlocked, "unlock"); err != nil {
		return err
	}
	// Operator contract: all writers (ExtendSchema) must be drained
	// before Unlock. If rows remain, something bypassed the state
	// machine. Dev builds surface this as an error; production logs a
	// warning and proceeds (the log is cleared unconditionally — the
	// caller has committed to leaving LOCKED state).
	tag, err := s.q.Exec(ctx, `
		DELETE FROM model_schema_extensions
		WHERE tenant_id = $1 AND model_name = $2 AND model_version = $3`,
		string(s.tenantID), modelRef.EntityName, modelRef.ModelVersion)
	if err != nil {
		return fmt.Errorf("Unlock: clear extension log for %s: %w", modelRef, err)
	}
	if n := tag.RowsAffected(); n != 0 {
		if buildIsDev() {
			return fmt.Errorf("Unlock found %d live extension rows for %s; operator-contract violation — concurrent writers did not drain before Unlock", n, modelRef)
		}
		slog.Warn("Unlock drained stale extension rows",
			"pkg", "postgres", "ref", modelRef, "count", n)
	}
	return nil
}

func (s *modelStore) IsLocked(ctx context.Context, modelRef spi.ModelRef) (bool, error) {
	var raw []byte
	err := s.q.QueryRow(ctx,
		`SELECT doc FROM models WHERE tenant_id = $1 AND model_name = $2 AND model_version = $3`,
		string(s.tenantID), modelRef.EntityName, modelRef.ModelVersion).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
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
	tag, err := s.q.Exec(ctx,
		`UPDATE models
		 SET doc = jsonb_set(doc, '{changeLevel}', to_jsonb($4::text))
		 WHERE tenant_id = $1 AND model_name = $2 AND model_version = $3`,
		string(s.tenantID), modelRef.EntityName, modelRef.ModelVersion, string(level))
	if err != nil {
		return fmt.Errorf("failed to set change level for model %s: %w", modelRef, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("model %s not found: %w", modelRef, spi.ErrNotFound)
	}
	return nil
}

// updateStateField updates the state field in the doc JSONB column for Lock/Unlock.
func (s *modelStore) updateStateField(ctx context.Context, modelRef spi.ModelRef, state spi.ModelState, op string) error {
	tag, err := s.q.Exec(ctx,
		`UPDATE models
		 SET doc = jsonb_set(doc, '{state}', to_jsonb($4::text))
		 WHERE tenant_id = $1 AND model_name = $2 AND model_version = $3`,
		string(s.tenantID), modelRef.EntityName, modelRef.ModelVersion, string(state))
	if err != nil {
		return fmt.Errorf("failed to %s model %s: %w", op, modelRef, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("model %s not found: %w", modelRef, spi.ErrNotFound)
	}
	return nil
}

// unmarshalModelDoc deserializes the JSONB doc column into a ModelDescriptor.
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

// ExtendSchema appends a delta row to model_schema_extensions. If the
// resulting seq is a multiple of 64, a savepoint row (the fully-folded
// schema as of the new seq) is inserted in the same transaction so
// a future Get can start from there rather than replaying the entire
// log. Empty or nil deltas are a no-op.
//
// The insert participates in the ambient entity transaction via the
// plugin's Querier resolution (ctxQuerier), so visibility is
// commit-bound.
func (s *modelStore) ExtendSchema(ctx context.Context, ref spi.ModelRef, delta spi.SchemaDelta) error {
	if len(delta) == 0 {
		return nil
	}

	txID := ""
	if tx := spi.GetTransaction(ctx); tx != nil {
		txID = tx.ID
	}

	var newSeq int64
	err := s.q.QueryRow(ctx,
		`INSERT INTO model_schema_extensions
		    (tenant_id, model_name, model_version, kind, payload, tx_id)
		 VALUES ($1, $2, $3, 'delta', $4, $5)
		 RETURNING seq`,
		string(s.tenantID), ref.EntityName, ref.ModelVersion, []byte(delta), txID).Scan(&newSeq)
	if err != nil {
		return fmt.Errorf("failed to append schema delta for %s: %w", ref, err)
	}

	if newSeq%64 == 0 {
		base, err := s.baseSchema(ctx, ref)
		if err != nil {
			return fmt.Errorf("savepoint base-schema read for %s: %w", ref, err)
		}
		folded, err := s.foldLocked(ctx, ref, base)
		if err != nil {
			return fmt.Errorf("savepoint fold for %s (seq=%d): %w", ref, newSeq, err)
		}
		if _, err := s.q.Exec(ctx,
			`INSERT INTO model_schema_extensions
			    (tenant_id, model_name, model_version, kind, payload, tx_id)
			 VALUES ($1, $2, $3, 'savepoint', $4, $5)`,
			string(s.tenantID), ref.EntityName, ref.ModelVersion, folded, txID); err != nil {
			return fmt.Errorf("failed to write savepoint for %s (seq=%d): %w", ref, newSeq, err)
		}
	}
	return nil
}

// baseSchema loads the stable base schema stored in models.doc.schema
// for the given ref. Used both by Get (pre-fold) and by ExtendSchema's
// savepoint branch.
func (s *modelStore) baseSchema(ctx context.Context, ref spi.ModelRef) ([]byte, error) {
	var raw []byte
	err := s.q.QueryRow(ctx,
		`SELECT doc FROM models WHERE tenant_id = $1 AND model_name = $2 AND model_version = $3`,
		string(s.tenantID), ref.EntityName, ref.ModelVersion).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("model %s not found: %w", ref, spi.ErrNotFound)
		}
		return nil, fmt.Errorf("base schema lookup for %s: %w", ref, err)
	}
	desc, err := unmarshalModelDoc(raw)
	if err != nil {
		return nil, err
	}
	return desc.Schema, nil
}
