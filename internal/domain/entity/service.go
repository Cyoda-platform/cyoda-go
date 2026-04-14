package entity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/internal/common"
	"github.com/cyoda-platform/cyoda-go/internal/domain/model/importer"
)

// --- Input/Output types ---

// CreateEntityInput holds parameters for creating an entity.
type CreateEntityInput struct {
	EntityName   string
	ModelVersion string
	Format       string
	Data         json.RawMessage
}

// EntityTransactionResult holds the result of an entity operation.
type EntityTransactionResult struct {
	TransactionID string
	EntityIDs     []string
}

// GetOneEntityInput holds parameters for getting an entity.
type GetOneEntityInput struct {
	EntityID    string
	PointInTime *time.Time
}

// EntityEnvelope holds a single entity in its response envelope format.
type EntityEnvelope struct {
	Type string
	Data any
	Meta map[string]any
}

// UpdateEntityInput holds parameters for updating an entity.
type UpdateEntityInput struct {
	EntityID   string
	Format     string
	Data       json.RawMessage
	Transition string // optional, empty for loopback
	IfMatch    string // optional ETag for CAS
}

// DeleteAllResult holds the result of deleting all entities for a model.
type DeleteAllResult struct {
	TotalCount    int
	ModelID       string
	EntityModelID string
}

// EntityChangeEntry holds a single entry in version history.
type EntityChangeEntry struct {
	ChangeType    string
	TimeOfChange  string
	User          string
	TransactionID string
	HasEntity     bool
}

// PaginationParams holds pagination parameters.
type PaginationParams struct {
	PageSize   int32
	PageNumber int32
}

// CollectionItem holds a parsed item for batch create.
type CollectionItem struct {
	ModelName    string
	ModelVersion int32
	Payload      json.RawMessage
}

// --- Service methods ---

// CreateEntity creates a single entity with workflow execution and returns
// the transaction result.
func (h *Handler) CreateEntity(ctx context.Context, input CreateEntityInput) (*EntityTransactionResult, error) {
	uc := spi.MustGetUserContext(ctx)

	modelStore, err := h.factory.ModelStore(ctx)
	if err != nil {
		return nil, common.Internal("failed to access model store", err)
	}

	ref := spi.ModelRef{
		EntityName:   input.EntityName,
		ModelVersion: input.ModelVersion,
	}

	// Load model descriptor
	desc, err := modelStore.Get(ctx, ref)
	if err != nil {
		return nil, common.Operational(http.StatusNotFound, common.ErrCodeModelNotFound, "model not found")
	}

	// Reject if model not locked
	if desc.State != spi.ModelLocked {
		return nil, common.Operational(http.StatusConflict, common.ErrCodeModelNotLocked, "model is not locked")
	}

	// Parse body based on format
	bodyBytes := []byte(input.Data)
	var parsedData any
	switch input.Format {
	case "JSON":
		if err := json.Unmarshal(bodyBytes, &parsedData); err != nil {
			return nil, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, "invalid JSON")
		}
	case "XML":
		parsed, err := importer.ParseXML(strings.NewReader(string(bodyBytes)))
		if err != nil {
			return nil, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, "invalid XML")
		}
		parsedData = parsed
		bodyBytes, err = json.Marshal(parsedData)
		if err != nil {
			return nil, common.Internal("failed to serialize parsed XML", err)
		}
	default:
		return nil, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, "unsupported format")
	}

	// Validate or extend model schema
	if err := h.validateOrExtend(ctx, modelStore, desc, parsedData); err != nil {
		return nil, classifyValidateOrExtendErr(err)
	}

	// Begin transaction
	txID, txCtx, err := h.txMgr.Begin(ctx)
	if err != nil {
		return nil, common.Internal("failed to begin transaction", err)
	}

	entityID := uuid.UUID(h.uuids.NewTimeUUID())
	now := time.Now()

	entity := &spi.Entity{
		Meta: spi.EntityMeta{
			ID:                      entityID.String(),
			TenantID:                uc.Tenant.ID,
			ModelRef:                ref,
			State:                   "",
			CreationDate:            now,
			LastModifiedDate:        now,
			TransactionID:           txID,
			TransitionForLatestSave: "",
			ChangeType:              "CREATED",
			ChangeUser:              uc.UserID,
		},
		Data: bodyBytes,
	}

	// Run workflow engine within transaction context.
	result, err := h.engine.Execute(txCtx, entity, "")
	if err != nil {
		h.txMgr.Rollback(txCtx, txID)
		slog.Error("workflow execution failed", "error", err.Error(), "entityId", entity.Meta.ID)
		return nil, common.Operational(http.StatusBadRequest, common.ErrCodeWorkflowFailed, err.Error())
	}

	// If no workflow was found, engine returns forced success and entity state stays empty.
	// Set a default state.
	if entity.Meta.State == "" {
		entity.Meta.State = "CREATED"
	}

	if result != nil && result.StopReason == "" {
		entity.Meta.TransitionForLatestSave = "workflow"
	}

	// Save entity within transaction (goes to buffer).
	entityStore, err := h.factory.EntityStore(txCtx)
	if err != nil {
		h.txMgr.Rollback(txCtx, txID)
		return nil, common.Internal("failed to access entity store", err)
	}
	if _, err := entityStore.Save(txCtx, entity); err != nil {
		h.txMgr.Rollback(txCtx, txID)
		return nil, common.Internal("failed to save entity", err)
	}

	// Commit transaction.
	if err := h.txMgr.Commit(txCtx, txID); err != nil {
		if errors.Is(err, spi.ErrConflict) {
			return nil, common.Conflict("transaction conflict — retry")
		}
		return nil, common.Internal("failed to commit transaction", err)
	}

	return &EntityTransactionResult{
		TransactionID: txID,
		EntityIDs:     []string{entityID.String()},
	}, nil
}

// GetEntity retrieves a single entity, optionally at a point in time.
func (h *Handler) GetEntity(ctx context.Context, input GetOneEntityInput) (*EntityEnvelope, error) {
	entityStore, err := h.factory.EntityStore(ctx)
	if err != nil {
		return nil, common.Internal("failed to access entity store", err)
	}

	var ent *spi.Entity
	if input.PointInTime != nil {
		ent, err = entityStore.GetAsAt(ctx, input.EntityID, *input.PointInTime)
	} else {
		ent, err = entityStore.Get(ctx, input.EntityID)
	}
	if err != nil {
		if errors.Is(err, spi.ErrNotFound) {
			appErr := common.Operational(http.StatusNotFound, common.ErrCodeEntityNotFound, fmt.Sprintf("entity id=%s not found", input.EntityID))
			appErr.Props = map[string]any{
				"entityId": input.EntityID,
			}
			return nil, appErr
		}
		return nil, common.Internal("failed to retrieve entity", err)
	}

	// Parse entity data to any for response
	var data any
	if err := json.Unmarshal(ent.Data, &data); err != nil {
		return nil, common.Internal("failed to parse entity data", err)
	}

	// Parse model version to int
	versionInt, _ := strconv.Atoi(ent.Meta.ModelRef.ModelVersion)

	meta := map[string]any{
		"id": ent.Meta.ID,
		"modelKey": map[string]any{
			"name":    ent.Meta.ModelRef.EntityName,
			"version": versionInt,
		},
		"state":          ent.Meta.State,
		"creationDate":   ent.Meta.CreationDate.UTC().Format(time.RFC3339Nano),
		"lastUpdateTime": ent.Meta.LastModifiedDate.UTC().Format(time.RFC3339Nano),
		"transactionId":  ent.Meta.TransactionID,
	}
	if ent.Meta.TransitionForLatestSave != "" {
		meta["transitionForLatestSave"] = ent.Meta.TransitionForLatestSave
	}

	return &EntityEnvelope{
		Type: "ENTITY",
		Data: data,
		Meta: meta,
	}, nil
}

// GetStatistics retrieves entity count statistics for all models.
func (h *Handler) GetStatistics(ctx context.Context) ([]EntityStat, error) {
	modelStore, err := h.factory.ModelStore(ctx)
	if err != nil {
		return nil, common.Internal("failed to access model store", err)
	}

	entityStore, err := h.factory.EntityStore(ctx)
	if err != nil {
		return nil, common.Internal("failed to access entity store", err)
	}

	refs, err := modelStore.GetAll(ctx)
	if err != nil {
		return nil, common.Internal("failed to list models", err)
	}

	result := make([]EntityStat, 0, len(refs))
	for _, ref := range refs {
		count, err := entityStore.Count(ctx, ref)
		if err != nil {
			return nil, common.Internal("failed to count entities", err)
		}
		result = append(result, EntityStat{
			ModelName:    ref.EntityName,
			ModelVersion: ref.ModelVersion,
			Count:        count,
		})
	}

	return result, nil
}

// EntityStat holds entity count for a model.
type EntityStat struct {
	ModelName    string
	ModelVersion string
	Count        int64
}

// EntityStatByState holds entity count for a model and state.
type EntityStatByState struct {
	ModelName    string
	ModelVersion string
	State        string
	Count        int64
}

// GetStatisticsByState retrieves entity count statistics by state for all models.
func (h *Handler) GetStatisticsByState(ctx context.Context, states *[]string) ([]EntityStatByState, error) {
	modelStore, err := h.factory.ModelStore(ctx)
	if err != nil {
		return nil, common.Internal("failed to access model store", err)
	}

	entityStore, err := h.factory.EntityStore(ctx)
	if err != nil {
		return nil, common.Internal("failed to access entity store", err)
	}

	refs, err := modelStore.GetAll(ctx)
	if err != nil {
		return nil, common.Internal("failed to list models", err)
	}

	result := make([]EntityStatByState, 0)
	for _, ref := range refs {
		entities, err := entityStore.GetAll(ctx, ref)
		if err != nil {
			return nil, common.Internal("failed to get entities", err)
		}

		stateCounts := make(map[string]int64)
		for _, ent := range entities {
			stateCounts[ent.Meta.State]++
		}

		for state, count := range stateCounts {
			if states != nil {
				found := false
				for _, s := range *states {
					if s == state {
						found = true
						break
					}
				}
				if !found {
					continue
				}
			}
			result = append(result, EntityStatByState{
				ModelName:    ref.EntityName,
				ModelVersion: ref.ModelVersion,
				State:        state,
				Count:        count,
			})
		}
	}

	return result, nil
}

// GetStatisticsByStateForModel retrieves entity count statistics by state for a specific model.
func (h *Handler) GetStatisticsByStateForModel(ctx context.Context, entityName string, modelVersion string, states *[]string) ([]EntityStatByState, error) {
	entityStore, err := h.factory.EntityStore(ctx)
	if err != nil {
		return nil, common.Internal("failed to access entity store", err)
	}

	ref := spi.ModelRef{
		EntityName:   entityName,
		ModelVersion: modelVersion,
	}

	entities, err := entityStore.GetAll(ctx, ref)
	if err != nil {
		return nil, common.Internal("failed to get entities", err)
	}

	stateCounts := make(map[string]int64)
	for _, ent := range entities {
		stateCounts[ent.Meta.State]++
	}

	result := make([]EntityStatByState, 0, len(stateCounts))
	for state, count := range stateCounts {
		if states != nil {
			found := false
			for _, s := range *states {
				if s == state {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		result = append(result, EntityStatByState{
			ModelName:    entityName,
			ModelVersion: modelVersion,
			State:        state,
			Count:        count,
		})
	}

	return result, nil
}

// GetStatisticsForModel retrieves entity count statistics for a specific model.
func (h *Handler) GetStatisticsForModel(ctx context.Context, entityName string, modelVersion string) (*EntityStat, error) {
	entityStore, err := h.factory.EntityStore(ctx)
	if err != nil {
		return nil, common.Internal("failed to access entity store", err)
	}

	ref := spi.ModelRef{
		EntityName:   entityName,
		ModelVersion: modelVersion,
	}

	count, err := entityStore.Count(ctx, ref)
	if err != nil {
		return nil, common.Internal("failed to count entities", err)
	}

	return &EntityStat{
		ModelName:    entityName,
		ModelVersion: modelVersion,
		Count:        count,
	}, nil
}

// DeleteEntity deletes a single entity by ID within a transaction.
// Returns the deleted entity's metadata for the response.
func (h *Handler) DeleteEntity(ctx context.Context, entityID string) (*deleteEntityResult, error) {
	// Begin transaction.
	txID, txCtx, err := h.txMgr.Begin(ctx)
	if err != nil {
		return nil, common.Internal("failed to begin transaction", err)
	}

	entityStore, err := h.factory.EntityStore(txCtx)
	if err != nil {
		h.txMgr.Rollback(txCtx, txID)
		return nil, common.Internal("failed to access entity store", err)
	}

	// Load entity before deleting to get ModelRef for response (adds to read set).
	entity, err := entityStore.Get(txCtx, entityID)
	if err != nil {
		h.txMgr.Rollback(txCtx, txID)
		appErr := common.Operational(http.StatusNotFound, common.ErrCodeEntityNotFound, fmt.Sprintf("entity id=%s not found", entityID))
		appErr.Props = map[string]any{
			"entityId": entityID,
		}
		return nil, appErr
	}

	// Soft delete within transaction.
	if err := entityStore.Delete(txCtx, entityID); err != nil {
		h.txMgr.Rollback(txCtx, txID)
		return nil, common.Internal("failed to delete entity", err)
	}

	// Commit transaction.
	if err := h.txMgr.Commit(txCtx, txID); err != nil {
		if errors.Is(err, spi.ErrConflict) {
			return nil, common.Conflict("transaction conflict — retry")
		}
		return nil, common.Internal("failed to commit transaction", err)
	}

	ver, _ := strconv.Atoi(entity.Meta.ModelRef.ModelVersion)
	return &deleteEntityResult{
		EntityID:      entityID,
		ModelName:     entity.Meta.ModelRef.EntityName,
		ModelVersion:  ver,
		TransactionID: txID,
	}, nil
}

type deleteEntityResult struct {
	EntityID      string
	ModelName     string
	ModelVersion  int
	TransactionID string
}

// GetChangesMetadata retrieves version history metadata for an entity.
func (h *Handler) GetChangesMetadata(ctx context.Context, entityID string) ([]EntityChangeEntry, error) {
	entityStore, err := h.factory.EntityStore(ctx)
	if err != nil {
		return nil, common.Internal("failed to access entity store", err)
	}

	versions, err := entityStore.GetVersionHistory(ctx, entityID)
	if err != nil {
		if errors.Is(err, spi.ErrNotFound) {
			appErr := common.Operational(http.StatusNotFound, common.ErrCodeEntityNotFound, fmt.Sprintf("entity id=%s not found", entityID))
			appErr.Props = map[string]any{
				"entityId": entityID,
			}
			return nil, appErr
		}
		return nil, common.Internal("failed to get version history", err)
	}

	// Sort newest first (descending by timestamp)
	sort.Slice(versions, func(i, j int) bool {
		return versions[i].Timestamp.After(versions[j].Timestamp)
	})

	// Hard cap to prevent unbounded response size.
	const maxChangesMetadata = 1000
	if len(versions) > maxChangesMetadata {
		versions = versions[:maxChangesMetadata]
	}

	result := make([]EntityChangeEntry, 0, len(versions))
	for _, v := range versions {
		entry := EntityChangeEntry{
			ChangeType:   v.ChangeType,
			TimeOfChange: v.Timestamp.UTC().Format(time.RFC3339Nano),
			User:         v.User,
			HasEntity:    v.Entity != nil,
		}
		if v.Entity != nil {
			entry.TransactionID = v.Entity.Meta.TransactionID
		}
		result = append(result, entry)
	}

	return result, nil
}

// DeleteAllEntities deletes all entities for a model within a transaction.
func (h *Handler) DeleteAllEntities(ctx context.Context, entityName string, modelVersion string) (*DeleteAllResult, error) {
	ref := spi.ModelRef{
		EntityName:   entityName,
		ModelVersion: modelVersion,
	}

	// Begin transaction.
	txID, txCtx, err := h.txMgr.Begin(ctx)
	if err != nil {
		return nil, common.Internal("failed to begin transaction", err)
	}

	entityStore, err := h.factory.EntityStore(txCtx)
	if err != nil {
		h.txMgr.Rollback(txCtx, txID)
		return nil, common.Internal("failed to access entity store", err)
	}

	// Get all entities before deleting (for verbose response and IDs).
	entities, err := entityStore.GetAll(txCtx, ref)
	if err != nil {
		h.txMgr.Rollback(txCtx, txID)
		return nil, common.Internal("failed to get entities", err)
	}

	if len(entities) == 0 {
		h.txMgr.Rollback(txCtx, txID)
		return nil, common.Operational(404, common.ErrCodeEntityNotFound,
			fmt.Sprintf("no entities found for model %s/%s", entityName, modelVersion))
	}

	if err := entityStore.DeleteAll(txCtx, ref); err != nil {
		h.txMgr.Rollback(txCtx, txID)
		return nil, common.Internal("failed to delete entities", err)
	}

	// Commit transaction.
	if err := h.txMgr.Commit(txCtx, txID); err != nil {
		if errors.Is(err, spi.ErrConflict) {
			return nil, common.Conflict("transaction conflict — retry")
		}
		return nil, common.Internal("failed to commit transaction", err)
	}

	modelID := deterministicModelID(ref)
	return &DeleteAllResult{
		TotalCount:    len(entities),
		ModelID:       modelID.String(),
		EntityModelID: modelID.String(),
	}, nil
}

// ListEntities retrieves all entities for a model with pagination.
func (h *Handler) ListEntities(ctx context.Context, entityName string, modelVersion string, pagination PaginationParams) ([]EntityEnvelope, error) {
	entityStore, err := h.factory.EntityStore(ctx)
	if err != nil {
		return nil, common.Internal("failed to access entity store", err)
	}

	ref := spi.ModelRef{
		EntityName:   entityName,
		ModelVersion: modelVersion,
	}

	entities, err := entityStore.GetAll(ctx, ref)
	if err != nil {
		return nil, common.Internal("failed to get entities", err)
	}

	// Sort by entity ID for deterministic pagination
	sort.Slice(entities, func(i, j int) bool {
		return entities[i].Meta.ID < entities[j].Meta.ID
	})

	// Apply pagination
	start := int(pagination.PageNumber * pagination.PageSize)
	if start > len(entities) {
		start = len(entities)
	}
	end := start + int(pagination.PageSize)
	if end > len(entities) {
		end = len(entities)
	}
	page := entities[start:end]

	// Build envelopes without modelKey in meta
	result := make([]EntityEnvelope, 0, len(page))
	for _, ent := range page {
		var data any
		if err := json.Unmarshal(ent.Data, &data); err != nil {
			return nil, common.Internal("failed to parse entity data", err)
		}

		entMeta := map[string]any{
			"id":             ent.Meta.ID,
			"state":          ent.Meta.State,
			"creationDate":   ent.Meta.CreationDate.UTC().Format(time.RFC3339Nano),
			"lastUpdateTime": ent.Meta.LastModifiedDate.UTC().Format(time.RFC3339Nano),
			"transactionId":  ent.Meta.TransactionID,
		}
		if ent.Meta.TransitionForLatestSave != "" {
			entMeta["transitionForLatestSave"] = ent.Meta.TransitionForLatestSave
		}

		result = append(result, EntityEnvelope{
			Type: "ENTITY",
			Data: data,
			Meta: entMeta,
		})
	}

	return result, nil
}

// CreateEntityCollection creates multiple entities in a single transaction.
func (h *Handler) CreateEntityCollection(ctx context.Context, items []CollectionItem) (*EntityTransactionResult, error) {
	uc := spi.MustGetUserContext(ctx)

	modelStore, err := h.factory.ModelStore(ctx)
	if err != nil {
		return nil, common.Internal("failed to access model store", err)
	}

	// Validate all items before starting the transaction.
	type parsedItem struct {
		ref          spi.ModelRef
		payloadBytes []byte
	}
	parsed := make([]parsedItem, 0, len(items))
	for i, item := range items {
		ref := spi.ModelRef{
			EntityName:   item.ModelName,
			ModelVersion: fmt.Sprintf("%d", item.ModelVersion),
		}

		// Load and validate model
		desc, err := modelStore.Get(ctx, ref)
		if err != nil {
			return nil, common.Operational(http.StatusNotFound, common.ErrCodeModelNotFound,
				fmt.Sprintf("item %d: model not found", i))
		}
		if desc.State != spi.ModelLocked {
			return nil, common.Operational(http.StatusConflict, common.ErrCodeModelNotLocked,
				fmt.Sprintf("item %d: model is not locked", i))
		}

		// Parse payload
		var parsedData any
		payloadBytes := []byte(item.Payload)
		if err := json.Unmarshal(payloadBytes, &parsedData); err != nil {
			return nil, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest,
				fmt.Sprintf("item %d: invalid JSON payload", i))
		}

		// Validate or extend model schema
		if err := h.validateOrExtend(ctx, modelStore, desc, parsedData); err != nil {
			return nil, classifyValidateOrExtendErr(err)
		}

		parsed = append(parsed, parsedItem{ref: ref, payloadBytes: payloadBytes})
	}

	// Begin transaction -- all entities in one transaction.
	txID, txCtx, err := h.txMgr.Begin(ctx)
	if err != nil {
		return nil, common.Internal("failed to begin transaction", err)
	}

	entityStore, err := h.factory.EntityStore(txCtx)
	if err != nil {
		h.txMgr.Rollback(txCtx, txID)
		return nil, common.Internal("failed to access entity store", err)
	}

	now := time.Now()

	// Pre-generate entity IDs so the iterator has no side effects.
	// This decouples ID generation from SaveAll's consumption pattern,
	// making it safe even if a future SaveAll consumes from multiple goroutines.
	entityIDs := make([]string, len(parsed))
	for i := range parsed {
		entityIDs[i] = uuid.UUID(h.uuids.NewTimeUUID()).String()
	}

	entities := func(yield func(*spi.Entity) bool) {
		for i, item := range parsed {
			entity := &spi.Entity{
				Meta: spi.EntityMeta{
					ID:               entityIDs[i],
					TenantID:         uc.Tenant.ID,
					ModelRef:         item.ref,
					State:            "CREATED",
					CreationDate:     now,
					LastModifiedDate: now,
					TransactionID:    txID,
					ChangeType:       "CREATED",
					ChangeUser:       uc.UserID,
				},
				Data: item.payloadBytes,
			}
			if !yield(entity) {
				return
			}
		}
	}

	if _, err := entityStore.SaveAll(txCtx, entities); err != nil {
		h.txMgr.Rollback(txCtx, txID)
		return nil, common.Internal("failed to save entities", err)
	}

	// Commit transaction.
	if err := h.txMgr.Commit(txCtx, txID); err != nil {
		if errors.Is(err, spi.ErrConflict) {
			return nil, common.Conflict("transaction conflict — retry")
		}
		return nil, common.Internal("failed to commit transaction", err)
	}

	return &EntityTransactionResult{
		TransactionID: txID,
		EntityIDs:     entityIDs,
	}, nil
}

// UpdateEntity updates a single entity with an optional named transition or loopback.
func (h *Handler) UpdateEntity(ctx context.Context, input UpdateEntityInput) (*EntityTransactionResult, error) {
	modelStore, err := h.factory.ModelStore(ctx)
	if err != nil {
		return nil, common.Internal("failed to access model store", err)
	}

	// Parse body based on format
	bodyBytes := []byte(input.Data)
	var parsedData any
	switch input.Format {
	case "JSON":
		if err := json.Unmarshal(bodyBytes, &parsedData); err != nil {
			return nil, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, "invalid JSON")
		}
	case "XML":
		parsed, err := importer.ParseXML(strings.NewReader(string(bodyBytes)))
		if err != nil {
			return nil, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, "invalid XML")
		}
		parsedData = parsed
		bodyBytes, err = json.Marshal(parsedData)
		if err != nil {
			return nil, common.Internal("failed to serialize parsed XML", err)
		}
	default:
		return nil, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, "unsupported format")
	}

	// Begin transaction.
	txID, txCtx, err := h.txMgr.Begin(ctx)
	if err != nil {
		return nil, common.Internal("failed to begin transaction", err)
	}

	// Load existing entity within transaction (adds to read set).
	entityStore, err := h.factory.EntityStore(txCtx)
	if err != nil {
		h.txMgr.Rollback(txCtx, txID)
		return nil, common.Internal("failed to access entity store", err)
	}

	existing, err := entityStore.Get(txCtx, input.EntityID)
	if err != nil {
		h.txMgr.Rollback(txCtx, txID)
		return nil, common.Operational(http.StatusNotFound, common.ErrCodeEntityNotFound, "entity not found")
	}

	// Load model descriptor
	desc, err := modelStore.Get(txCtx, existing.Meta.ModelRef)
	if err != nil {
		h.txMgr.Rollback(txCtx, txID)
		return nil, common.Internal("failed to load model for entity", err)
	}

	// Validate or extend model schema
	if err := h.validateOrExtend(txCtx, modelStore, desc, parsedData); err != nil {
		h.txMgr.Rollback(txCtx, txID)
		return nil, classifyValidateOrExtendErr(err)
	}

	now := time.Now()

	uc := spi.GetUserContext(ctx)
	changeUser := ""
	if uc != nil {
		changeUser = uc.UserID
	}

	updated := &spi.Entity{
		Meta: spi.EntityMeta{
			ID:                      existing.Meta.ID,
			TenantID:                existing.Meta.TenantID,
			ModelRef:                existing.Meta.ModelRef,
			State:                   existing.Meta.State,
			Version:                 existing.Meta.Version,
			CreationDate:            existing.Meta.CreationDate,
			LastModifiedDate:        now,
			TransactionID:           txID,
			ChangeType:              "UPDATED",
			ChangeUser:              changeUser,
			TransitionForLatestSave: input.Transition,
		},
		Data: bodyBytes,
	}

	// Execute workflow: loopback or named manual transition.
	if input.Transition == "" {
		// Loopback
		if _, err := h.engine.Loopback(txCtx, updated); err != nil {
			h.txMgr.Rollback(txCtx, txID)
			slog.Error("workflow loopback failed", "error", err.Error(), "entityId", updated.Meta.ID)
			return nil, common.Operational(http.StatusBadRequest, common.ErrCodeWorkflowFailed, err.Error())
		}
		updated.Meta.TransitionForLatestSave = "loopback"
	} else {
		// Named manual transition
		if _, err := h.engine.ManualTransition(txCtx, updated, input.Transition); err != nil {
			h.txMgr.Rollback(txCtx, txID)
			slog.Error("workflow manual transition failed", "error", err.Error(), "entityId", updated.Meta.ID, "transition", input.Transition)
			return nil, common.Operational(http.StatusBadRequest, common.ErrCodeWorkflowFailed, err.Error())
		}
		updated.Meta.TransitionForLatestSave = input.Transition
	}

	// Atomic MVCC save: if If-Match was provided, use CompareAndSave for atomicity.
	if input.IfMatch != "" {
		if _, err := entityStore.CompareAndSave(txCtx, updated, input.IfMatch); err != nil {
			h.txMgr.Rollback(txCtx, txID)
			if errors.Is(err, spi.ErrConflict) {
				appErr := common.Conflict("entity has been modified since last read")
				appErr.Status = http.StatusPreconditionFailed
				appErr.Props = map[string]any{
					"entityId": input.EntityID,
				}
				return nil, appErr
			}
			return nil, common.Internal("failed to save entity", err)
		}
	} else {
		if _, err := entityStore.Save(txCtx, updated); err != nil {
			h.txMgr.Rollback(txCtx, txID)
			return nil, common.Internal("failed to save entity", err)
		}
	}

	// Commit transaction.
	if err := h.txMgr.Commit(txCtx, txID); err != nil {
		if errors.Is(err, spi.ErrConflict) {
			return nil, common.Conflict("transaction conflict — retry")
		}
		return nil, common.Internal("failed to commit transaction", err)
	}

	return &EntityTransactionResult{
		TransactionID: txID,
		EntityIDs:     []string{input.EntityID},
	}, nil
}

// classifyError converts an error to an *common.AppError if it isn't already one.
func classifyError(err error) *common.AppError {
	var appErr *common.AppError
	if errors.As(err, &appErr) {
		return appErr
	}
	return common.Internal("unexpected error", err)
}
