package dispatch

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/internal/contract"
)

var ErrHMACSecretTooShort = errors.New("HMAC secret must be at least 32 bytes")

const maxDispatchBodySize = 10 * 1024 * 1024 // 10MB

// DispatchHandler serves the internal dispatch endpoints for processor and criteria
// execution. Requests are authenticated with HMAC-SHA256.
type DispatchHandler struct {
	local      contract.ExternalProcessingService
	hmacSecret []byte
}

// NewDispatchHandler constructs a DispatchHandler backed by the given local
// ExternalProcessingService and HMAC secret. The secret must be at least 32 bytes.
func NewDispatchHandler(local contract.ExternalProcessingService, hmacSecret []byte) (*DispatchHandler, error) {
	if len(hmacSecret) < 32 {
		return nil, ErrHMACSecretTooShort
	}
	return &DispatchHandler{
		local:      local,
		hmacSecret: hmacSecret,
	}, nil
}

// Register registers the dispatch routes on the provided ServeMux.
func (h *DispatchHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /internal/dispatch/processor", h.handleProcessor)
	mux.HandleFunc("POST /internal/dispatch/criteria", h.handleCriteria)
}

// handleProcessor handles POST /internal/dispatch/processor.
func (h *DispatchHandler) handleProcessor(w http.ResponseWriter, r *http.Request) {
	body, ok := h.readAndVerify(w, r)
	if !ok {
		return
	}

	var req DispatchProcessorRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	ctx := h.buildContext(r, req.TenantID, req.UserID, req.Roles)

	entity := &spi.Entity{
		Meta: req.EntityMeta,
		Data: []byte(req.Entity),
	}

	result, err := h.local.DispatchProcessor(ctx, entity, req.Processor, req.WorkflowName, req.TransitionName, req.TxID)
	if err != nil {
		slog.Error("dispatch processor failed", "pkg", "dispatch", "err", err)
		writeJSON(w, http.StatusOK, DispatchProcessorResponse{
			Success: false,
			Error:   "dispatch processor failed",
		})
		return
	}

	writeJSON(w, http.StatusOK, DispatchProcessorResponse{
		Success:    true,
		EntityData: result.Data,
	})
}

// handleCriteria handles POST /internal/dispatch/criteria.
func (h *DispatchHandler) handleCriteria(w http.ResponseWriter, r *http.Request) {
	body, ok := h.readAndVerify(w, r)
	if !ok {
		return
	}

	var req DispatchCriteriaRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	ctx := h.buildContext(r, req.TenantID, req.UserID, req.Roles)

	entity := &spi.Entity{
		Meta: req.EntityMeta,
		Data: []byte(req.Entity),
	}

	matches, err := h.local.DispatchCriteria(ctx, entity, req.Criterion, req.Target, req.WorkflowName, req.TransitionName, req.ProcessorName, req.TxID)
	if err != nil {
		slog.Error("dispatch criteria failed", "pkg", "dispatch", "err", err)
		writeJSON(w, http.StatusOK, DispatchCriteriaResponse{
			Success: false,
			Error:   "dispatch criteria failed",
		})
		return
	}

	writeJSON(w, http.StatusOK, DispatchCriteriaResponse{
		Success: true,
		Matches: matches,
	})
}

// readAndVerify reads the full request body, verifies the HMAC-SHA256 signature
// from the X-Dispatch-HMAC header, and returns the body bytes. On failure it
// writes an error response and returns false.
func (h *DispatchHandler) readAndVerify(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	sig := r.Header.Get("X-Dispatch-HMAC")
	if sig == "" {
		slog.Warn("dispatch request missing HMAC header", "pkg", "dispatch", "remoteAddr", r.RemoteAddr)
		http.Error(w, "forbidden", http.StatusForbidden)
		return nil, false
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxDispatchBodySize)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusInternalServerError)
		return nil, false
	}

	expected := h.sign(body)
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		slog.Warn("dispatch request HMAC mismatch", "pkg", "dispatch", "remoteAddr", r.RemoteAddr)
		http.Error(w, "forbidden", http.StatusForbidden)
		return nil, false
	}

	return body, true
}

// sign returns the hex-encoded HMAC-SHA256 of body — identical to HTTPForwarder.sign().
func (h *DispatchHandler) sign(body []byte) string {
	mac := hmac.New(sha256.New, h.hmacSecret)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// buildContext constructs a context.Context carrying the UserContext from the
// dispatch request fields.
func (h *DispatchHandler) buildContext(r *http.Request, tenantID, userID string, roles []string) context.Context {
	uc := &spi.UserContext{
		UserID: userID,
		Tenant: spi.Tenant{
			ID: spi.TenantID(tenantID),
		},
		Roles: roles,
	}
	return spi.WithUserContext(r.Context(), uc)
}

// writeJSON encodes v as JSON and writes it with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("dispatch handler: failed to write JSON response", "pkg", "dispatch", "err", err)
	}
}
