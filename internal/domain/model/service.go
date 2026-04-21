package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/internal/common"
	"github.com/cyoda-platform/cyoda-go/internal/domain/model/exporter"
	"github.com/cyoda-platform/cyoda-go/internal/domain/model/importer"
	"github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
)

// ImportModelInput carries the parameters for importing a model.
type ImportModelInput struct {
	EntityName   string
	ModelVersion string
	Format       string
	Converter    string
	Data         []byte
}

// ImportModelResult carries the result of a model import.
type ImportModelResult struct {
	ModelID string
}

// ExportModelResult carries the result of a model export.
type ExportModelResult struct {
	Payload json.RawMessage
}

// ModelTransitionResult carries the result of a model state transition.
type ModelTransitionResult struct {
	ModelID string
	State   string
}

// ModelInfo carries summary information about a model.
type ModelInfo struct {
	ID         string
	Name       string
	Version    int
	State      string
	UpdateDate time.Time
}

// parseVersion converts a string model version to int32.
func parseVersion(v string) int32 {
	n, _ := strconv.ParseInt(v, 10, 32)
	return int32(n)
}

// getModelFresh returns the model descriptor, bypassing any per-request
// cache layer when the store supports RefreshAndGet. In multi-node
// cluster deployments this eliminates a stale-cache race window
// between a peer's mutation and its gossip-borne invalidation.
// Admin-path gating reads (ImportModel, LockModel, UnlockModel,
// DeleteModel, SetChangeLevel) use this; routine display/listing
// reads (ExportModel, ListModels, ValidateModel) keep the cache for
// throughput — eventual consistency is acceptable for those paths.
func getModelFresh(ctx context.Context, store spi.ModelStore, ref spi.ModelRef) (*spi.ModelDescriptor, error) {
	type refresher interface {
		RefreshAndGet(ctx context.Context, ref spi.ModelRef) (*spi.ModelDescriptor, error)
	}
	if r, ok := store.(refresher); ok {
		return r.RefreshAndGet(ctx, ref)
	}
	return store.Get(ctx, ref)
}

// ImportModel imports a model from sample data, merging with any existing schema.
func (h *Handler) ImportModel(ctx context.Context, input ImportModelInput) (*ImportModelResult, error) {
	if input.Converter != "SAMPLE_DATA" {
		return nil, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, "unsupported import converter")
	}

	store, err := h.factory.ModelStore(ctx)
	if err != nil {
		return nil, common.Internal("failed to access model store", err)
	}

	ver := parseVersion(input.ModelVersion)
	ref := modelRef(input.EntityName, ver)

	// Bypass the per-request cache: in a multi-node cluster the cache
	// can briefly serve a stale LOCKED descriptor in the window between a
	// peer's delete and its gossip-borne invalidation. Admin operations
	// are low-frequency, so one forced round-trip is fine.
	existing, err := getModelFresh(ctx, store, ref)
	if err != nil {
		existing = nil
	}

	if existing != nil && existing.State == spi.ModelLocked {
		appErr := common.Conflict(
			fmt.Sprintf("cannot save entityModel{name=%s, version=%d} because this model has already been registered", input.EntityName, ver))
		appErr.Props = map[string]any{
			"entityName":    input.EntityName,
			"entityVersion": ver,
		}
		return nil, appErr
	}

	newNode, err := importer.NewSampleDataImporter().Import(
		bytes.NewReader(input.Data), input.Format)
	if err != nil {
		return nil, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, err.Error())
	}

	var finalNode *schema.ModelNode
	if existing != nil && len(existing.Schema) > 0 {
		existingNode, err := schema.Unmarshal(existing.Schema)
		if err != nil {
			return nil, common.Internal("failed to unmarshal existing schema", err)
		}
		finalNode = schema.Merge(existingNode, newNode)
	} else {
		finalNode = newNode
	}

	schemaBytes, err := schema.Marshal(finalNode)
	if err != nil {
		return nil, common.Internal("failed to marshal schema", err)
	}

	desc := &spi.ModelDescriptor{
		Ref:        ref,
		State:      spi.ModelUnlocked,
		UpdateDate: time.Now(),
		Schema:     schemaBytes,
	}
	if existing != nil {
		desc.ChangeLevel = existing.ChangeLevel
	}

	if err := store.Save(ctx, desc); err != nil {
		return nil, common.Internal("failed to save model", err)
	}

	return &ImportModelResult{ModelID: deterministicID(ref).String()}, nil
}

// ExportModel exports a model schema using the specified converter.
func (h *Handler) ExportModel(ctx context.Context, entityName, modelVersion, converter string) (*ExportModelResult, error) {
	store, err := h.factory.ModelStore(ctx)
	if err != nil {
		return nil, common.Internal("failed to access model store", err)
	}

	ver := parseVersion(modelVersion)
	ref := modelRef(entityName, ver)
	desc, err := store.Get(ctx, ref)
	if err != nil {
		return nil, modelNotFound(entityName, ver)
	}

	node, err := schema.Unmarshal(desc.Schema)
	if err != nil {
		return nil, common.Internal("failed to unmarshal schema", err)
	}

	var exp exporter.Exporter
	switch converter {
	case "JSON_SCHEMA":
		exp = exporter.NewJSONSchemaExporter(string(desc.State))
	case "SIMPLE_VIEW":
		exp = exporter.NewSimpleViewExporter(string(desc.State))
	default:
		return nil, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, "unsupported export converter")
	}

	exported, err := exp.Export(node)
	if err != nil {
		return nil, common.Internal("export failed", err)
	}

	return &ExportModelResult{Payload: exported}, nil
}

// LockModel locks a model, preventing further imports.
func (h *Handler) LockModel(ctx context.Context, entityName, modelVersion string) (*ModelTransitionResult, error) {
	store, err := h.factory.ModelStore(ctx)
	if err != nil {
		return nil, common.Internal("failed to access model store", err)
	}

	ver := parseVersion(modelVersion)
	ref := modelRef(entityName, ver)

	// Admin-path gating read — bypass the per-request cache; see
	// getModelFresh for the multi-node rationale.
	desc, err := getModelFresh(ctx, store, ref)
	if err != nil || desc == nil {
		return nil, modelNotFound(entityName, ver)
	}

	if desc.State == spi.ModelLocked {
		appErr := common.Conflict(
			fmt.Sprintf("cannot process entityModel{entityName=%s, entityVersion=%d}. expectedState=UNLOCKED, actualState=LOCKED", entityName, ver))
		appErr.Props = map[string]any{
			"entityName":    entityName,
			"entityVersion": ver,
			"expectedState": "UNLOCKED",
			"actualState":   "LOCKED",
		}
		return nil, appErr
	}

	if err := store.Lock(ctx, ref); err != nil {
		return nil, common.Internal("failed to lock model", err)
	}

	return &ModelTransitionResult{
		ModelID: deterministicID(ref).String(),
		State:   "LOCKED",
	}, nil
}

// UnlockModel unlocks a model, allowing further imports. Blocked if entities exist.
func (h *Handler) UnlockModel(ctx context.Context, entityName, modelVersion string) (*ModelTransitionResult, error) {
	modelStore, err := h.factory.ModelStore(ctx)
	if err != nil {
		return nil, common.Internal("failed to access model store", err)
	}

	ver := parseVersion(modelVersion)
	ref := modelRef(entityName, ver)

	// Admin-path gating read — bypass the per-request cache; see
	// getModelFresh for the multi-node rationale.
	desc, err := getModelFresh(ctx, modelStore, ref)
	if err != nil || desc == nil {
		return nil, modelNotFound(entityName, ver)
	}

	if desc.State != spi.ModelLocked {
		return nil, common.Conflict("model is not locked")
	}

	entityStore, err := h.factory.EntityStore(ctx)
	if err != nil {
		return nil, common.Internal("failed to access entity store", err)
	}

	count, err := entityStore.Count(ctx, ref)
	if err != nil {
		return nil, common.Internal("failed to count entities", err)
	}
	if count > 0 {
		return nil, common.Conflict(
			fmt.Sprintf("cannot unlock: %d entities exist", count))
	}

	if err := modelStore.Unlock(ctx, ref); err != nil {
		return nil, common.Internal("failed to unlock model", err)
	}

	return &ModelTransitionResult{
		ModelID: deterministicID(ref).String(),
		State:   "UNLOCKED",
	}, nil
}

// DeleteModel deletes a model. Blocked if model is locked or entities exist.
func (h *Handler) DeleteModel(ctx context.Context, entityName, modelVersion string) error {
	modelStore, err := h.factory.ModelStore(ctx)
	if err != nil {
		return common.Internal("failed to access model store", err)
	}

	ver := parseVersion(modelVersion)
	ref := modelRef(entityName, ver)

	// Admin-path gating read — bypass the per-request cache; see
	// getModelFresh for the multi-node rationale.
	desc, err := getModelFresh(ctx, modelStore, ref)
	if err != nil || desc == nil {
		return modelNotFound(entityName, ver)
	}

	entityStore, err := h.factory.EntityStore(ctx)
	if err != nil {
		return common.Internal("failed to access entity store", err)
	}

	count, err := entityStore.Count(ctx, ref)
	if err != nil {
		return common.Internal("failed to count entities", err)
	}
	if count > 0 {
		return common.Conflict(
			fmt.Sprintf("cannot delete: %d entities exist", count))
	}

	if err := modelStore.Delete(ctx, ref); err != nil {
		return common.Internal("failed to delete model", err)
	}

	return nil
}

// ListModels returns summary information for all models.
func (h *Handler) ListModels(ctx context.Context) ([]ModelInfo, error) {
	store, err := h.factory.ModelStore(ctx)
	if err != nil {
		return nil, common.Internal("failed to access model store", err)
	}

	refs, err := store.GetAll(ctx)
	if err != nil {
		return nil, common.Internal("failed to list models", err)
	}

	models := make([]ModelInfo, 0, len(refs))
	for _, ref := range refs {
		desc, err := store.Get(ctx, ref)
		if err != nil {
			return nil, common.Internal("failed to load model", err)
		}

		ver, _ := strconv.ParseInt(ref.ModelVersion, 10, 32)
		models = append(models, ModelInfo{
			ID:         deterministicID(ref).String(),
			Name:       ref.EntityName,
			Version:    int(ver),
			State:      string(desc.State),
			UpdateDate: desc.UpdateDate,
		})
	}

	return models, nil
}

// ValidateModel validates data against a model's schema.
func (h *Handler) ValidateModel(ctx context.Context, entityName, modelVersion string, data json.RawMessage) error {
	store, err := h.factory.ModelStore(ctx)
	if err != nil {
		return common.Internal("failed to access model store", err)
	}

	ver := parseVersion(modelVersion)
	ref := modelRef(entityName, ver)
	desc, err := store.Get(ctx, ref)
	if err != nil {
		return modelNotFound(entityName, ver)
	}

	modelNode, err := schema.Unmarshal(desc.Schema)
	if err != nil {
		return common.Internal("failed to unmarshal schema", err)
	}

	var parsedData any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&parsedData); err != nil {
		return common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, "failed to parse request body")
	}

	validationErrors := schema.Validate(modelNode, parsedData)
	if len(validationErrors) == 0 {
		return nil
	}

	msgs := make([]string, len(validationErrors))
	for i, ve := range validationErrors {
		msgs[i] = ve.Error()
	}
	return &ValidationError{
		Message: "Validation failed: " + strings.Join(msgs, "; "),
	}
}

// SetChangeLevel sets the change level on a model.
func (h *Handler) SetChangeLevel(ctx context.Context, entityName, modelVersion, changeLevel string) error {
	store, err := h.factory.ModelStore(ctx)
	if err != nil {
		return common.Internal("failed to access model store", err)
	}

	ver := parseVersion(modelVersion)
	ref := modelRef(entityName, ver)

	// Admin-path gating read — bypass the per-request cache; see
	// getModelFresh for the multi-node rationale.
	if desc, err := getModelFresh(ctx, store, ref); err != nil || desc == nil {
		return modelNotFound(entityName, ver)
	}

	cl, err := spi.ValidateChangeLevel(changeLevel)
	if err != nil {
		return common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, err.Error())
	}

	if err := store.SetChangeLevel(ctx, ref, cl); err != nil {
		return common.Internal("failed to set change level", err)
	}

	return nil
}

// ValidationError is a non-AppError that signals validation failure (not an HTTP error).
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string { return e.Message }
