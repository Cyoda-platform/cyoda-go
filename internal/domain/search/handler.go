package search

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go-spi/predicate"
	genapi "github.com/cyoda-platform/cyoda-go/api"
	"github.com/cyoda-platform/cyoda-go/internal/common"
)

const maxSearchBodySize = 10 * 1024 * 1024 // 10 MiB

// Handler handles search-related HTTP endpoints.
type Handler struct {
	searchSvc *SearchService
}

// NewHandler creates a new search handler wired to the given SearchService.
func NewHandler(searchSvc *SearchService) *Handler {
	return &Handler{searchSvc: searchSvc}
}

// ---------------------------------------------------------------------------
// Direct (synchronous) search
// ---------------------------------------------------------------------------

func (h *Handler) SearchEntities(w http.ResponseWriter, r *http.Request, entityName string, modelVersion int32, params genapi.SearchEntitiesParams) {
	r.Body = http.MaxBytesReader(w, r.Body, maxSearchBodySize)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		common.WriteError(w, r, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, "failed to read request body"))
		return
	}

	cond, err := predicate.ParseCondition(body)
	if err != nil {
		common.WriteError(w, r, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, fmt.Sprintf("invalid condition: %v", err)))
		return
	}

	opts := SearchOptions{
		PointInTime: params.PointInTime,
	}

	// Parse limit from string parameter.
	if params.Limit != nil {
		lim, err := strconv.Atoi(*params.Limit)
		if err != nil || lim < 0 {
			common.WriteError(w, r, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, "invalid limit"))
			return
		}
		if lim > 10000 {
			lim = 10000
		}
		opts.Limit = lim
	}

	modelRef := spi.ModelRef{
		EntityName:   entityName,
		ModelVersion: fmt.Sprintf("%d", modelVersion),
	}

	results, err := h.searchSvc.Search(r.Context(), modelRef, cond, opts)
	if err != nil {
		common.WriteError(w, r, common.Internal("search failed", err))
		return
	}

	// Per canonical openapi-entity-search.yml line 587, sync search returns
	// application/x-ndjson — a stream of EntityResult JSON objects, one per line.
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	for _, e := range results {
		if err := enc.Encode(entityEnvelope(e)); err != nil {
			// Header is already written; we can only log and stop. The
			// client sees a truncated stream and a connection error,
			// which is the correct failure mode for a streaming endpoint.
			slog.Error("ndjson encode failed mid-stream",
				"pkg", "search", "error", err.Error())
			return
		}
	}
}

// ---------------------------------------------------------------------------
// Async search: submit
// ---------------------------------------------------------------------------

func (h *Handler) SubmitAsyncSearchJob(w http.ResponseWriter, r *http.Request, entityName string, modelVersion int32, params genapi.SubmitAsyncSearchJobParams) {
	r.Body = http.MaxBytesReader(w, r.Body, maxSearchBodySize)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		common.WriteError(w, r, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, "failed to read request body"))
		return
	}

	cond, err := predicate.ParseCondition(body)
	if err != nil {
		common.WriteError(w, r, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, fmt.Sprintf("invalid condition: %v", err)))
		return
	}

	opts := SearchOptions{
		PointInTime: params.PointInTime,
	}

	modelRef := spi.ModelRef{
		EntityName:   entityName,
		ModelVersion: fmt.Sprintf("%d", modelVersion),
	}

	jobID, err := h.searchSvc.SubmitAsync(r.Context(), modelRef, cond, opts)
	if err != nil {
		common.WriteError(w, r, common.Internal("failed to submit async search", err))
		return
	}

	// Return bare job ID string (matches Cyoda Cloud response).
	common.WriteJSON(w, http.StatusOK, jobID)
}

// ---------------------------------------------------------------------------
// Async search: status
// ---------------------------------------------------------------------------

func (h *Handler) GetAsyncSearchStatus(w http.ResponseWriter, r *http.Request, jobId openapi_types.UUID) {
	status, err := h.searchSvc.GetAsyncStatus(r.Context(), jobId.String())
	if err != nil {
		common.WriteError(w, r, common.Operational(http.StatusNotFound, common.ErrCodeEntityNotFound, fmt.Sprintf("job not found: %v", err)))
		return
	}

	resp := buildStatusResponse(status)
	common.WriteJSON(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------------
// Async search: results
// ---------------------------------------------------------------------------

func (h *Handler) GetAsyncSearchResults(w http.ResponseWriter, r *http.Request, jobId openapi_types.UUID, params genapi.GetAsyncSearchResultsParams) {
	opts := ResultOptions{}

	pageSize := 1000 // default
	if params.PageSize != nil {
		ps, err := strconv.Atoi(*params.PageSize)
		if err != nil || ps < 0 {
			common.WriteError(w, r, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, "invalid pageSize"))
			return
		}
		opts.Limit = ps
		pageSize = ps
	}

	pageNumber := 0
	if params.PageNumber != nil {
		pn, err := strconv.Atoi(*params.PageNumber)
		if err != nil || pn < 0 {
			common.WriteError(w, r, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, "invalid pageNumber"))
			return
		}
		pageNumber = pn
		// Convert page number to offset: offset = pageNumber * limit.
		if pageSize <= 0 {
			pageSize = 1000
		}
		opts.Offset = pn * pageSize
	}

	page, err := h.searchSvc.GetAsyncResults(r.Context(), jobId.String(), opts)
	if err != nil {
		common.WriteError(w, r, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, fmt.Sprintf("failed to get results: %v", err)))
		return
	}

	envelopes := make([]map[string]any, 0, len(page.Results))
	for _, e := range page.Results {
		envelopes = append(envelopes, entityEnvelope(e))
	}

	if pageSize <= 0 {
		pageSize = 1000
	}
	totalPages := 0
	if page.Total > 0 {
		totalPages = (page.Total + pageSize - 1) / pageSize
	}

	resp := map[string]any{
		"content": envelopes,
		"page": map[string]any{
			"number":        pageNumber,
			"size":          pageSize,
			"totalElements": page.Total,
			"totalPages":    totalPages,
		},
	}

	common.WriteJSON(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------------
// Async search: cancel
// ---------------------------------------------------------------------------

func (h *Handler) CancelAsyncSearch(w http.ResponseWriter, r *http.Request, jobId openapi_types.UUID) {
	result, err := h.searchSvc.CancelAsync(r.Context(), jobId.String())
	if err != nil {
		common.WriteError(w, r, common.Operational(http.StatusNotFound, common.ErrCodeEntityNotFound, fmt.Sprintf("job not found: %v", err)))
		return
	}

	if !result.Cancelled {
		// Job was already completed (SUCCESSFUL/FAILED) — Cloud returns 400.
		resp := map[string]any{
			"detail":     fmt.Sprintf("snapshot by id=%s is not running. current status=%s", jobId.String(), result.CurrentStatus),
			"properties": map[string]any{"currentStatus": result.CurrentStatus, "snapshotId": jobId.String()},
			"status":     400,
			"title":      "Bad Request",
			"type":       "about:blank",
		}
		common.WriteJSON(w, http.StatusBadRequest, resp)
		return
	}

	resp := map[string]any{
		"isCancelled":            true,
		"cancelled":              true,
		"currentSearchJobStatus": result.CurrentStatus,
	}

	common.WriteJSON(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func buildStatusResponse(status SearchJobStatus) map[string]any {
	resp := map[string]any{
		"searchJobStatus":       status.Status,
		"createTime":            status.CreateTime.UTC().Format(time.RFC3339Nano),
		"entitiesCount":         status.Total,
		"calculationTimeMillis": status.CalcTimeMs,
		"expirationDate":        status.CreateTime.Add(24 * time.Hour).UTC().Format(time.RFC3339Nano),
	}
	if status.FinishTime != nil {
		resp["finishTime"] = status.FinishTime.UTC().Format(time.RFC3339Nano)
	}
	return resp
}

func entityEnvelope(e *spi.Entity) map[string]any {
	meta := map[string]any{
		"id":             e.Meta.ID,
		"state":          e.Meta.State,
		"creationDate":   e.Meta.CreationDate.UTC().Format(time.RFC3339Nano),
		"lastUpdateTime": e.Meta.LastModifiedDate.UTC().Format(time.RFC3339Nano),
	}
	if e.Meta.TransactionID != "" {
		meta["transactionId"] = e.Meta.TransactionID
	}
	if e.Meta.TransitionForLatestSave != "" {
		meta["transitionForLatestSave"] = e.Meta.TransitionForLatestSave
	}

	var data any
	dec := json.NewDecoder(bytes.NewReader(e.Data))
	dec.UseNumber()
	_ = dec.Decode(&data)

	return map[string]any{
		"type": "ENTITY",
		"data": data,
		"meta": meta,
	}
}
