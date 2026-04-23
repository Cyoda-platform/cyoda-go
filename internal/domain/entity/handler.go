package entity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	genapi "github.com/cyoda-platform/cyoda-go/api"
	"github.com/cyoda-platform/cyoda-go/internal/common"
	"github.com/cyoda-platform/cyoda-go/internal/domain/model/importer"
	"github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
	wfengine "github.com/cyoda-platform/cyoda-go/internal/domain/workflow"
)

// maxEntityBodySize is the maximum allowed request body size for entity operations (10 MB).
const maxEntityBodySize = 10 * 1024 * 1024

// errInternalSchema tags schema-processing errors inside validateOrExtend
// that represent internal failures (codec decode/encode, Diff computation,
// plugin-layer ExtendSchema write) rather than client-contract violations.
// The handler classifier uses errors.Is to route these to 5xx with a
// logged ticket. Using a sentinel rather than string-matching the wrap
// messages makes classification robust to future wording changes — the
// prior string-match classifier would have silently shifted a renamed
// "failed to extend schema" to 4xx.
var errInternalSchema = errors.New("internal schema processing failure")

// maxStatesFilterSize bounds the cardinality of the user-supplied ?states= query
// parameter on stats-by-state endpoints. Without this cap, an oversized list would
// reach SQL backends and either exceed driver parameter limits (SQLite's
// SQLITE_MAX_VARIABLE_NUMBER, default 32766) or stress the planner with a giant
// IN/ANY clause, surfacing as an opaque 5xx instead of a clean 4xx.
const maxStatesFilterSize = 1000

// deterministicModelID derives a stable UUID v5 from a ModelRef, matching the
// model handler's deterministic ID generation.
func deterministicModelID(ref spi.ModelRef) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(ref.String()))
}

type Handler struct {
	factory spi.StoreFactory
	txMgr   spi.TransactionManager
	uuids   spi.UUIDGenerator
	engine  *wfengine.Engine
}

func New(factory spi.StoreFactory, txMgr spi.TransactionManager, uuids spi.UUIDGenerator, engine *wfengine.Engine) *Handler {
	return &Handler{factory: factory, txMgr: txMgr, uuids: uuids, engine: engine}
}

func (h *Handler) stub(w http.ResponseWriter, r *http.Request) {
	common.WriteError(w, r, common.Operational(http.StatusNotImplemented, common.ErrCodeBadRequest, "not yet implemented"))
}

// validateOrExtend validates parsedData against the model schema. When
// changeLevel is set, it computes an additive schema delta via schema.Diff
// and appends it to the model's extension log via ModelStore.ExtendSchema.
// That call participates in the ambient entity transaction, so visibility
// is commit-bound and concurrent entity writes on the same model do not
// contend on a single "models" row — the hot-row regression that
// ModelStore.Save would otherwise produce under REPEATABLE READ.
// Returns an error on validation or extension failure.
func (h *Handler) validateOrExtend(ctx context.Context, modelStore spi.ModelStore, desc *spi.ModelDescriptor, parsedData any) error {
	modelNode, err := schema.Unmarshal(desc.Schema)
	if err != nil {
		return fmt.Errorf("%w: failed to unmarshal model schema: %w", errInternalSchema, err)
	}

	if desc.ChangeLevel == "" {
		errs := schema.Validate(modelNode, parsedData)
		if len(errs) > 0 {
			msgs := make([]string, len(errs))
			for i, e := range errs {
				msgs[i] = e.Error()
			}
			return fmt.Errorf("validation failed: %s", strings.Join(msgs, "; "))
		}
		return nil
	}

	incomingModel, err := importer.Walk(parsedData)
	if err != nil {
		return fmt.Errorf("failed to walk data: %w", err)
	}
	extended, err := schema.Extend(modelNode, incomingModel, desc.ChangeLevel)
	if err != nil {
		// Polymorphic-slot rejections cannot be resolved by raising ChangeLevel
		// and so must not wear the "change level violation" prefix — the phrase
		// misleads clients into tuning a setting that wouldn't help.
		if errors.Is(err, schema.ErrPolymorphicSlot) {
			return err
		}
		return fmt.Errorf("change level violation: %w", err)
	}

	// Compute the additive delta. Diff returns (nil, nil) when the
	// extension is a semantic no-op, which is the common case on
	// every entity write.
	delta, err := schema.Diff(modelNode, extended)
	if err != nil {
		return fmt.Errorf("%w: failed to compute schema delta: %w", errInternalSchema, err)
	}
	if delta == nil {
		return nil
	}
	// Append to the extension log via the plugin. Participates in the
	// ambient entity transaction so visibility is commit-bound.
	if err := modelStore.ExtendSchema(ctx, desc.Ref, delta); err != nil {
		return fmt.Errorf("%w: failed to extend schema: %w", errInternalSchema, err)
	}
	return nil
}

// ValidateWithRefresh runs strict schema validation with a bounded
// refresh-on-stale safety net. One refresh attempt, only on unknown-
// schema-element errors — the signal that our cached schema is behind
// a peer's ExtendSchema. Other validation failures surface directly.
// Stores that don't implement RefreshAndGet (no caching layer) skip
// the refresh and return the original errors. See spec §4.3.
func (h *Handler) ValidateWithRefresh(ctx context.Context, modelStore spi.ModelStore, ref spi.ModelRef, data any) error {
	desc, err := modelStore.Get(ctx, ref)
	if err != nil {
		return err
	}
	errs := validateDescriptor(desc, data)
	if errs == nil {
		return nil
	}
	if !schema.HasUnknownSchemaElement(errs) {
		return validationErrorsToError(errs)
	}
	refresher, ok := modelStore.(interface {
		RefreshAndGet(context.Context, spi.ModelRef) (*spi.ModelDescriptor, error)
	})
	if !ok {
		return validationErrorsToError(errs) // plugin has no cache
	}
	freshDesc, rErr := refresher.RefreshAndGet(ctx, ref)
	if rErr != nil {
		return rErr
	}
	if errs2 := validateDescriptor(freshDesc, data); errs2 != nil {
		return validationErrorsToError(errs2)
	}
	return nil
}

// validateDescriptor unmarshals desc.Schema and runs schema.Validate.
// Returns nil on success, or a []ValidationError on failure (including
// a descriptive entry if desc itself is malformed or nil).
func validateDescriptor(desc *spi.ModelDescriptor, data any) []schema.ValidationError {
	if desc == nil {
		return []schema.ValidationError{{Message: "nil descriptor"}}
	}
	node, err := schema.Unmarshal(desc.Schema)
	if err != nil {
		return []schema.ValidationError{{Message: fmt.Sprintf("unmarshal schema: %v", err)}}
	}
	return schema.Validate(node, data)
}

// validationErrorsToError converts a []ValidationError to a single error,
// preserving the concatenation style used by validateOrExtend.
func validationErrorsToError(errs []schema.ValidationError) error {
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	return fmt.Errorf("validation failed: %s", strings.Join(msgs, "; "))
}

// classifyValidateOrExtendErr determines whether a validateOrExtend error is
// internal (5xx) or operational (4xx) and returns the appropriate AppError.
//
// Classification is sentinel-based to keep it robust against wording drift
// in the wrap strings:
//
//   - ErrPolymorphicSlot → 4xx POLYMORPHIC_SLOT (client normalizes payload)
//   - errInternalSchema  → 5xx with logged ticket (codec/diff/store failure)
//   - anything else      → 4xx BAD_REQUEST (change-level violation,
//                          validation failure, malformed walk input)
func classifyValidateOrExtendErr(err error) *common.AppError {
	if errors.Is(err, schema.ErrPolymorphicSlot) {
		return common.Operational(http.StatusBadRequest, common.ErrCodePolymorphicSlot, err.Error())
	}
	if errors.Is(err, errInternalSchema) {
		return common.Internal("failed to process model schema", err)
	}
	return common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, err.Error())
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request, format genapi.CreateParamsFormat, entityName string, modelVersion int32, params genapi.CreateParams) {
	// Read request body (with size limit)
	r.Body = http.MaxBytesReader(w, r.Body, maxEntityBodySize)
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		common.WriteError(w, r, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, "failed to read body"))
		return
	}

	// Detect JSON array body — delegate to collection create, one entity per element.
	if string(format) == "JSON" && len(bodyBytes) > 0 && bodyBytes[0] == '[' {
		var rawItems []json.RawMessage
		if err := json.Unmarshal(bodyBytes, &rawItems); err != nil {
			common.WriteError(w, r, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, "invalid JSON array"))
			return
		}

		items := make([]CollectionItem, 0, len(rawItems))
		for _, raw := range rawItems {
			items = append(items, CollectionItem{
				ModelName:    entityName,
				ModelVersion: modelVersion,
				Payload:      raw,
			})
		}

		result, err := h.CreateEntityCollection(r.Context(), items)
		if err != nil {
			common.WriteError(w, r, classifyError(err))
			return
		}

		resp := map[string]any{
			"transactionId": result.TransactionID,
			"entityIds":     result.EntityIDs,
		}
		common.WriteJSON(w, http.StatusOK, []any{resp})
		return
	}

	result, err := h.CreateEntity(r.Context(), CreateEntityInput{
		EntityName:   entityName,
		ModelVersion: fmt.Sprintf("%d", modelVersion),
		Format:       string(format),
		Data:         bodyBytes,
	})
	if err != nil {
		common.WriteError(w, r, classifyError(err))
		return
	}

	resp := map[string]any{
		"transactionId": result.TransactionID,
		"entityIds":     result.EntityIDs,
	}
	common.WriteJSON(w, http.StatusOK, []any{resp})
}

func (h *Handler) GetOneEntity(w http.ResponseWriter, r *http.Request, entityId openapi_types.UUID, params genapi.GetOneEntityParams) {
	// Reject if both pointInTime and transactionId are set
	if params.PointInTime != nil && params.TransactionId != nil {
		common.WriteError(w, r, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, "cannot specify both pointInTime and transactionId"))
		return
	}

	input := GetOneEntityInput{
		EntityID:    entityId.String(),
		PointInTime: params.PointInTime,
	}

	envelope, err := h.GetEntity(r.Context(), input)
	if err != nil {
		common.WriteError(w, r, classifyError(err))
		return
	}

	resp := map[string]any{
		"type": envelope.Type,
		"data": envelope.Data,
		"meta": envelope.Meta,
	}
	common.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) GetEntityStatistics(w http.ResponseWriter, r *http.Request, params genapi.GetEntityStatisticsParams) {
	stats, err := h.GetStatistics(r.Context())
	if err != nil {
		common.WriteError(w, r, classifyError(err))
		return
	}

	result := make([]genapi.ModelStatsDto, 0, len(stats))
	for _, s := range stats {
		ver, _ := strconv.Atoi(s.ModelVersion)
		result = append(result, genapi.ModelStatsDto{
			ModelName:    s.ModelName,
			ModelVersion: int32(ver),
			Count:        s.Count,
		})
	}

	common.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) GetEntityStatisticsByState(w http.ResponseWriter, r *http.Request, params genapi.GetEntityStatisticsByStateParams) {
	if params.States != nil && len(*params.States) > maxStatesFilterSize {
		common.WriteError(w, r, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest,
			fmt.Sprintf("states filter has %d entries; maximum is %d", len(*params.States), maxStatesFilterSize)))
		return
	}
	stats, err := h.GetStatisticsByState(r.Context(), params.States)
	if err != nil {
		common.WriteError(w, r, classifyError(err))
		return
	}

	result := make([]genapi.ModelStateStatsDto, 0, len(stats))
	for _, s := range stats {
		ver, _ := strconv.Atoi(s.ModelVersion)
		result = append(result, genapi.ModelStateStatsDto{
			ModelName:    s.ModelName,
			ModelVersion: int32(ver),
			State:        s.State,
			Count:        s.Count,
		})
	}

	common.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) GetEntityStatisticsByStateForModel(w http.ResponseWriter, r *http.Request, entityName string, modelVersion int32, params genapi.GetEntityStatisticsByStateForModelParams) {
	if params.States != nil && len(*params.States) > maxStatesFilterSize {
		common.WriteError(w, r, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest,
			fmt.Sprintf("states filter has %d entries; maximum is %d", len(*params.States), maxStatesFilterSize)))
		return
	}
	stats, err := h.GetStatisticsByStateForModel(r.Context(), entityName, fmt.Sprintf("%d", modelVersion), params.States)
	if err != nil {
		common.WriteError(w, r, classifyError(err))
		return
	}

	result := make([]genapi.ModelStateStatsDto, 0, len(stats))
	for _, s := range stats {
		result = append(result, genapi.ModelStateStatsDto{
			ModelName:    s.ModelName,
			ModelVersion: modelVersion,
			State:        s.State,
			Count:        s.Count,
		})
	}

	common.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) GetEntityStatisticsForModel(w http.ResponseWriter, r *http.Request, entityName string, modelVersion int32, params genapi.GetEntityStatisticsForModelParams) {
	stat, err := h.GetStatisticsForModel(r.Context(), entityName, fmt.Sprintf("%d", modelVersion))
	if err != nil {
		common.WriteError(w, r, classifyError(err))
		return
	}

	result := genapi.ModelStatsDto{
		ModelName:    stat.ModelName,
		ModelVersion: modelVersion,
		Count:        stat.Count,
	}

	common.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) DeleteSingleEntity(w http.ResponseWriter, r *http.Request, entityId openapi_types.UUID) {
	result, err := h.DeleteEntity(r.Context(), entityId.String())
	if err != nil {
		common.WriteError(w, r, classifyError(err))
		return
	}

	resp := map[string]any{
		"id": result.EntityID,
		"modelKey": map[string]any{
			"name":    result.ModelName,
			"version": result.ModelVersion,
		},
		"transactionId": result.TransactionID,
	}
	common.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) GetEntityChangesMetadata(w http.ResponseWriter, r *http.Request, entityId openapi_types.UUID, params genapi.GetEntityChangesMetadataParams) {
	entries, err := h.GetChangesMetadata(r.Context(), entityId.String())
	if err != nil {
		common.WriteError(w, r, classifyError(err))
		return
	}

	result := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		entry := map[string]any{
			"changeType":   e.ChangeType,
			"timeOfChange": e.TimeOfChange,
			"user":         e.User,
		}
		if e.HasEntity {
			entry["transactionId"] = e.TransactionID
		}
		result = append(result, entry)
	}

	common.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) DeleteEntities(w http.ResponseWriter, r *http.Request, entityName string, modelVersion int32, params genapi.DeleteEntitiesParams) {
	result, err := h.DeleteAllEntities(r.Context(), entityName, fmt.Sprintf("%d", modelVersion))
	if err != nil {
		common.WriteError(w, r, classifyError(err))
		return
	}

	resp := []map[string]any{
		{
			"deleteResult": map[string]any{
				"idToError":                map[string]any{},
				"numberOfEntitites":        result.TotalCount,
				"numberOfEntititesRemoved": result.TotalCount,
			},
			"entityModelClassId": result.EntityModelID,
		},
	}
	common.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) GetAllEntities(w http.ResponseWriter, r *http.Request, entityName string, modelVersion int32, params genapi.GetAllEntitiesParams) {
	// Apply pagination defaults
	pageSize := int32(20)
	pageNumber := int32(0)
	if params.PageSize != nil {
		pageSize = *params.PageSize
	}
	if params.PageNumber != nil {
		pageNumber = *params.PageNumber
	}

	envelopes, err := h.ListEntities(r.Context(), entityName, fmt.Sprintf("%d", modelVersion), PaginationParams{
		PageSize:   pageSize,
		PageNumber: pageNumber,
	})
	if err != nil {
		common.WriteError(w, r, classifyError(err))
		return
	}

	result := make([]map[string]any, 0, len(envelopes))
	for _, env := range envelopes {
		result = append(result, map[string]any{
			"type": env.Type,
			"data": env.Data,
			"meta": env.Meta,
		})
	}

	common.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) CreateCollection(w http.ResponseWriter, r *http.Request, format genapi.CreateCollectionParamsFormat, params genapi.CreateCollectionParams) {
	// Read raw body and parse as JSON array (with size limit).
	r.Body = http.MaxBytesReader(w, r.Body, maxEntityBodySize)
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		common.WriteError(w, r, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, "failed to read body"))
		return
	}

	var rawItems []struct {
		Model struct {
			Name    string `json:"name"`
			Version int32  `json:"version"`
		} `json:"model"`
		Payload string `json:"payload"`
	}
	if err := json.Unmarshal(bodyBytes, &rawItems); err != nil {
		common.WriteError(w, r, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, "invalid JSON array"))
		return
	}

	items := make([]CollectionItem, 0, len(rawItems))
	for _, raw := range rawItems {
		items = append(items, CollectionItem{
			ModelName:    raw.Model.Name,
			ModelVersion: raw.Model.Version,
			Payload:      json.RawMessage(raw.Payload),
		})
	}

	result, err := h.CreateEntityCollection(r.Context(), items)
	if err != nil {
		common.WriteError(w, r, classifyError(err))
		return
	}

	resp := map[string]any{
		"transactionId": result.TransactionID,
		"entityIds":     result.EntityIDs,
	}
	common.WriteJSON(w, http.StatusOK, []any{resp})
}

func (h *Handler) UpdateCollection(w http.ResponseWriter, r *http.Request, format genapi.UpdateCollectionParamsFormat, params genapi.UpdateCollectionParams) {
	h.stub(w, r)
}

func (h *Handler) UpdateSingleWithLoopback(w http.ResponseWriter, r *http.Request, format genapi.UpdateSingleWithLoopbackParamsFormat, entityId openapi_types.UUID, params genapi.UpdateSingleWithLoopbackParams) {
	// Read request body (with size limit) -- outside transaction.
	r.Body = http.MaxBytesReader(w, r.Body, maxEntityBodySize)
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		common.WriteError(w, r, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, "failed to read body"))
		return
	}

	ifMatch := ""
	if params.IfMatch != nil {
		ifMatch = *params.IfMatch
	}

	result, err := h.UpdateEntity(r.Context(), UpdateEntityInput{
		EntityID:   entityId.String(),
		Format:     string(format),
		Data:       bodyBytes,
		Transition: "", // loopback
		IfMatch:    ifMatch,
	})
	if err != nil {
		common.WriteError(w, r, classifyError(err))
		return
	}

	resp := map[string]any{
		"transactionId": result.TransactionID,
		"entityIds":     result.EntityIDs,
	}
	common.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) UpdateSingle(w http.ResponseWriter, r *http.Request, format genapi.UpdateSingleParamsFormat, entityId openapi_types.UUID, transition string, params genapi.UpdateSingleParams) {
	// Read request body (with size limit) -- outside transaction.
	r.Body = http.MaxBytesReader(w, r.Body, maxEntityBodySize)
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		common.WriteError(w, r, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, "failed to read body"))
		return
	}

	ifMatch := ""
	if params.IfMatch != nil {
		ifMatch = *params.IfMatch
	}

	result, err := h.UpdateEntity(r.Context(), UpdateEntityInput{
		EntityID:   entityId.String(),
		Format:     string(format),
		Data:       bodyBytes,
		Transition: transition,
		IfMatch:    ifMatch,
	})
	if err != nil {
		common.WriteError(w, r, classifyError(err))
		return
	}

	resp := map[string]any{
		"transactionId": result.TransactionID,
		"entityIds":     result.EntityIDs,
	}
	common.WriteJSON(w, http.StatusOK, resp)
}
