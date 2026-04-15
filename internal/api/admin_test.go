package api_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go/internal/api"
	"github.com/cyoda-platform/cyoda-go/logging"
	"github.com/cyoda-platform/cyoda-go/observability"
)

// adminContext returns a request with a ROLE_ADMIN UserContext attached.
func adminContext(req *http.Request) *http.Request {
	uc := &spi.UserContext{
		UserID:   "test-admin",
		UserName: "admin",
		Tenant:   spi.Tenant{ID: "test-tenant", Name: "Test"},
		Roles:    []string{"ROLE_ADMIN"},
	}
	return req.WithContext(spi.WithUserContext(req.Context(), uc))
}

func TestHandleGetLogLevel(t *testing.T) {
	// Set a known level
	logging.Level.Set(slog.LevelInfo)

	req := adminContext(httptest.NewRequest(http.MethodGet, "/admin/log-level", nil))
	rec := httptest.NewRecorder()

	api.HandleGetLogLevel(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["level"] != "info" {
		t.Fatalf("expected level info, got %q", body["level"])
	}
}

func TestHandleGetLogLevel_Forbidden(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/admin/log-level", nil)
	rec := httptest.NewRecorder()

	api.HandleGetLogLevel(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestHandleSetLogLevel(t *testing.T) {
	// Start at INFO
	logging.Level.Set(slog.LevelInfo)

	payload := `{"level":"debug"}`
	req := adminContext(httptest.NewRequest(http.MethodPost, "/admin/log-level", strings.NewReader(payload)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.HandleSetLogLevel(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["previous"] != "info" {
		t.Fatalf("expected previous info, got %q", body["previous"])
	}
	if body["level"] != "debug" {
		t.Fatalf("expected level debug, got %q", body["level"])
	}

	// Verify level actually changed
	if logging.LevelString(logging.Level.Level()) != "debug" {
		t.Fatal("level was not changed")
	}

	// Reset for other tests
	logging.Level.Set(slog.LevelInfo)
}

func TestHandleSetLogLevel_Forbidden(t *testing.T) {
	payload := `{"level":"debug"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/log-level", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.HandleSetLogLevel(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestHandleGetLogLevel_Forbidden_RFC9457(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/admin/log-level", nil)
	rec := httptest.NewRecorder()

	api.HandleGetLogLevel(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}

	// Should be RFC 9457 problem+json, not raw JSON
	ct := rec.Header().Get("Content-Type")
	if ct != "application/problem+json" {
		t.Errorf("expected Content-Type application/problem+json, got %q", ct)
	}

	var pd map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&pd); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if pd["status"] != float64(403) {
		t.Errorf("expected status 403 in body, got %v", pd["status"])
	}
}

func TestHandleSetLogLevel_Forbidden_RFC9457(t *testing.T) {
	payload := `{"level":"debug"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/log-level", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.HandleSetLogLevel(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/problem+json" {
		t.Errorf("expected Content-Type application/problem+json, got %q", ct)
	}
}

func TestHandleSetLogLevel_BadBody_RFC9457(t *testing.T) {
	payload := `not json`
	req := adminContext(httptest.NewRequest(http.MethodPost, "/admin/log-level", strings.NewReader(payload)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.HandleSetLogLevel(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/problem+json" {
		t.Errorf("expected Content-Type application/problem+json, got %q", ct)
	}
}

func TestHandleSetLogLevel_EmptyLevel_RFC9457(t *testing.T) {
	payload := `{"level":""}`
	req := adminContext(httptest.NewRequest(http.MethodPost, "/admin/log-level", strings.NewReader(payload)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.HandleSetLogLevel(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/problem+json" {
		t.Errorf("expected Content-Type application/problem+json, got %q", ct)
	}
}

func TestHandleSetLogLevel_EmptyLevel(t *testing.T) {
	payload := `{"level":""}`
	req := adminContext(httptest.NewRequest(http.MethodPost, "/admin/log-level", strings.NewReader(payload)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.HandleSetLogLevel(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleGetTraceSampler(t *testing.T) {
	prev := observability.Sampler.Config()
	t.Cleanup(func() { _ = observability.Sampler.SetSampler(prev) })

	if err := observability.Sampler.SetSampler(observability.SamplerConfig{
		Sampler: "ratio", Ratio: 0.1, ParentBased: true,
	}); err != nil {
		t.Fatalf("SetSampler: %v", err)
	}

	req := adminContext(httptest.NewRequest(http.MethodGet, "/admin/trace-sampler", nil))
	rec := httptest.NewRecorder()

	api.HandleGetTraceSampler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var got observability.SamplerConfig
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := observability.SamplerConfig{Sampler: "ratio", Ratio: 0.1, ParentBased: true}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestHandleGetTraceSampler_Forbidden(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/admin/trace-sampler", nil)
	rec := httptest.NewRecorder()

	api.HandleGetTraceSampler(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "application/problem+json" {
		t.Errorf("expected Content-Type application/problem+json, got %q", ct)
	}
}

func TestHandleSetTraceSampler_Always(t *testing.T) {
	prev := observability.Sampler.Config()
	t.Cleanup(func() { _ = observability.Sampler.SetSampler(prev) })

	payload := `{"sampler":"always"}`
	req := adminContext(httptest.NewRequest(http.MethodPost, "/admin/trace-sampler", strings.NewReader(payload)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.HandleSetTraceSampler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	got := observability.Sampler.Config()
	want := observability.SamplerConfig{Sampler: "always", ParentBased: true}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestHandleSetTraceSampler_Ratio(t *testing.T) {
	prev := observability.Sampler.Config()
	t.Cleanup(func() { _ = observability.Sampler.SetSampler(prev) })

	payload := `{"sampler":"ratio","ratio":0.1}`
	req := adminContext(httptest.NewRequest(http.MethodPost, "/admin/trace-sampler", strings.NewReader(payload)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.HandleSetTraceSampler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	got := observability.Sampler.Config()
	want := observability.SamplerConfig{Sampler: "ratio", Ratio: 0.1, ParentBased: true}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestHandleSetTraceSampler_ParentBasedFalse(t *testing.T) {
	prev := observability.Sampler.Config()
	t.Cleanup(func() { _ = observability.Sampler.SetSampler(prev) })

	payload := `{"sampler":"always","parent_based":false}`
	req := adminContext(httptest.NewRequest(http.MethodPost, "/admin/trace-sampler", strings.NewReader(payload)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.HandleSetTraceSampler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	got := observability.Sampler.Config()
	if got.ParentBased {
		t.Errorf("ParentBased = true, want false (explicitly set)")
	}
}

func TestHandleSetTraceSampler_ParentBasedDefault(t *testing.T) {
	prev := observability.Sampler.Config()
	t.Cleanup(func() { _ = observability.Sampler.SetSampler(prev) })

	// Omit parent_based — should default to true.
	payload := `{"sampler":"always"}`
	req := adminContext(httptest.NewRequest(http.MethodPost, "/admin/trace-sampler", strings.NewReader(payload)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.HandleSetTraceSampler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	got := observability.Sampler.Config()
	if !got.ParentBased {
		t.Errorf("ParentBased = false, want true (default)")
	}
}

func TestHandleSetTraceSampler_InvalidSamplerType(t *testing.T) {
	prev := observability.Sampler.Config()
	t.Cleanup(func() { _ = observability.Sampler.SetSampler(prev) })

	payload := `{"sampler":"foo"}`
	req := adminContext(httptest.NewRequest(http.MethodPost, "/admin/trace-sampler", strings.NewReader(payload)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.HandleSetTraceSampler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "application/problem+json" {
		t.Errorf("expected Content-Type application/problem+json, got %q", ct)
	}
}

func TestHandleSetTraceSampler_RatioOnNonRatio(t *testing.T) {
	prev := observability.Sampler.Config()
	t.Cleanup(func() { _ = observability.Sampler.SetSampler(prev) })

	payload := `{"sampler":"always","ratio":0.1}`
	req := adminContext(httptest.NewRequest(http.MethodPost, "/admin/trace-sampler", strings.NewReader(payload)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.HandleSetTraceSampler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleSetTraceSampler_RatioOutOfRange(t *testing.T) {
	prev := observability.Sampler.Config()
	t.Cleanup(func() { _ = observability.Sampler.SetSampler(prev) })

	payload := `{"sampler":"ratio","ratio":1.5}`
	req := adminContext(httptest.NewRequest(http.MethodPost, "/admin/trace-sampler", strings.NewReader(payload)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.HandleSetTraceSampler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleSetTraceSampler_RatioZero(t *testing.T) {
	prev := observability.Sampler.Config()
	t.Cleanup(func() { _ = observability.Sampler.SetSampler(prev) })

	payload := `{"sampler":"ratio","ratio":0}`
	req := adminContext(httptest.NewRequest(http.MethodPost, "/admin/trace-sampler", strings.NewReader(payload)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.HandleSetTraceSampler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (ratio=0 should be rejected; use sampler=never for zero sampling), got %d", rec.Code)
	}
}

func TestHandleSetTraceSampler_Forbidden(t *testing.T) {
	payload := `{"sampler":"always"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/trace-sampler", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.HandleSetTraceSampler(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestHandleSetTraceSampler_BadBody(t *testing.T) {
	prev := observability.Sampler.Config()
	t.Cleanup(func() { _ = observability.Sampler.SetSampler(prev) })

	payload := `not-json`
	req := adminContext(httptest.NewRequest(http.MethodPost, "/admin/trace-sampler", strings.NewReader(payload)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.HandleSetTraceSampler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}
