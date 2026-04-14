package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/cyoda-platform/cyoda-go/internal/common"
	"github.com/cyoda-platform/cyoda-go/internal/logging"
	"github.com/cyoda-platform/cyoda-go/internal/observability"
)

// HandleGetLogLevel returns the current log level as JSON.
func HandleGetLogLevel(w http.ResponseWriter, r *http.Request) {
	uc := common.GetUserContext(r.Context())
	if uc == nil || !common.HasRole(uc.Roles, "ROLE_ADMIN") {
		common.WriteError(w, r, common.Operational(http.StatusForbidden, common.ErrCodeForbidden, "admin role required"))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{
		"level": logging.LevelString(logging.Level.Level()),
	}); err != nil {
		slog.Debug("failed to encode response", "error", err)
	}
}

// HandleGetTraceSampler returns the current OTel trace sampler configuration.
// Requires ROLE_ADMIN. The response is round-trippable via POST.
func HandleGetTraceSampler(w http.ResponseWriter, r *http.Request) {
	uc := common.GetUserContext(r.Context())
	if uc == nil || !common.HasRole(uc.Roles, "ROLE_ADMIN") {
		common.WriteError(w, r, common.Operational(http.StatusForbidden, common.ErrCodeForbidden, "admin role required"))
		return
	}

	cfg := observability.Sampler.Config()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(cfg); err != nil {
		slog.Debug("failed to encode response", "error", err)
	}
}

// HandleSetTraceSampler changes the runtime OTel trace sampler configuration.
// Requires ROLE_ADMIN. Returns the new configuration on success.
//
// Note: when parent_based is true (the default), upstream traceparent
// sampling decisions are honored — "sampler: always" does NOT force 100%
// capture of all spans if upstream has already decided "do not sample".
// Set parent_based: false to override upstream decisions locally.
func HandleSetTraceSampler(w http.ResponseWriter, r *http.Request) {
	uc := common.GetUserContext(r.Context())
	if uc == nil || !common.HasRole(uc.Roles, "ROLE_ADMIN") {
		common.WriteError(w, r, common.Operational(http.StatusForbidden, common.ErrCodeForbidden, "admin role required"))
		return
	}

	// Pointer fields distinguish omitted from explicit false/zero.
	var req struct {
		Sampler     string   `json:"sampler"`
		Ratio       *float64 `json:"ratio,omitempty"`
		ParentBased *bool    `json:"parent_based,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.WriteError(w, r, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, "invalid request body"))
		return
	}

	// Build the canonical SamplerConfig.
	// - parent_based defaults to true when the field is omitted.
	// - ratio is only allowed when sampler == "ratio".
	pb := true
	if req.ParentBased != nil {
		pb = *req.ParentBased
	}

	cfg := observability.SamplerConfig{
		Sampler:     req.Sampler,
		ParentBased: pb,
	}
	if req.Ratio != nil {
		cfg.Ratio = *req.Ratio
	}

	// BuildSampler validates the config (ratio range, ratio-vs-type, etc.)
	// and returns an error for any invalid combination. SetSampler runs
	// BuildSampler again internally; the double-check is cheap and keeps
	// the handler logic symmetric with the Init path.
	if _, err := observability.BuildSampler(cfg); err != nil {
		common.WriteError(w, r, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, err.Error()))
		return
	}

	previous := observability.Sampler.Config()
	if err := observability.Sampler.SetSampler(cfg); err != nil {
		common.WriteError(w, r, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, err.Error()))
		return
	}

	slog.Info("trace sampler changed",
		"previous_sampler", previous.Sampler,
		"previous_ratio", previous.Ratio,
		"previous_parent_based", previous.ParentBased,
		"sampler", cfg.Sampler,
		"ratio", cfg.Ratio,
		"parent_based", cfg.ParentBased,
	)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(cfg); err != nil {
		slog.Debug("failed to encode response", "error", err)
	}
}

// HandleSetLogLevel changes the runtime log level and returns the previous and current levels.
func HandleSetLogLevel(w http.ResponseWriter, r *http.Request) {
	uc := common.GetUserContext(r.Context())
	if uc == nil || !common.HasRole(uc.Roles, "ROLE_ADMIN") {
		common.WriteError(w, r, common.Operational(http.StatusForbidden, common.ErrCodeForbidden, "admin role required"))
		return
	}

	var req struct {
		Level string `json:"level"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.WriteError(w, r, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, "invalid request body"))
		return
	}
	if req.Level == "" {
		common.WriteError(w, r, common.Operational(http.StatusBadRequest, common.ErrCodeBadRequest, "level is required"))
		return
	}

	previous := logging.LevelString(logging.Level.Level())
	logging.Level.Set(logging.ParseLevel(req.Level))
	current := logging.LevelString(logging.Level.Level())

	slog.Info("log level changed", "previous", previous, "current", current)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{
		"level":    current,
		"previous": previous,
	}); err != nil {
		slog.Debug("failed to encode response", "error", err)
	}
}
