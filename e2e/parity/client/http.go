package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Client is the parity HTTP client. It wraps net/http and adds
// DisallowUnknownFields decoding to catch API drift.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// NewClient constructs a Client targeting the given cyoda HTTP base URL,
// authenticated with the given JWT bearer token. The HTTP client uses
// a 30-second timeout per request — long enough for slow-processor
// tests but bounded so a hung request fails the test rather than
// hanging the suite.
func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// reqOption configures a single doJSON request. Future per-method
// options (custom Accept header, raw body sink, etc.) implement this
// interface and are passed as variadic args. No options are defined
// in Task 1.3; this is the seam future methods will use.
type reqOption func(*reqConfig)

type reqConfig struct {
	// Reserved for future per-method options. Currently unused.
}

// decodeJSONResponse decodes a successful HTTP response body into out
// using DisallowUnknownFields. Skips decoding when out is nil. Treats
// an empty body (io.EOF immediately) as "nothing to decode" rather
// than an error so endpoints that legitimately return 200 with no
// body work correctly. Draining the body before return enables
// connection reuse by the underlying transport. The caller is
// responsible for closing the response body.
func decodeJSONResponse(resp *http.Response, out any) error {
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	dec := json.NewDecoder(resp.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			// Empty body with a non-nil out — endpoint returned no
			// content. Treat as "nothing to decode" rather than an
			// error. This also handles chunked responses where
			// ContentLength == -1 and the body is empty.
			return nil
		}
		return fmt.Errorf("decode response (status %d): %w", resp.StatusCode, err)
	}
	return nil
}

// doJSON issues an HTTP request, optionally with a JSON body, and
// decodes the response into out (if non-nil) using DisallowUnknownFields.
// Returns the HTTP status code and any transport, status, or decode error.
// Non-2xx responses are returned as errors that include the captured
// response body so cyoda's JSON error envelopes are visible in
// parity-test output. The opts parameter is a seam for future per-method
// options (custom Accept header, raw body sink, etc.).
func (c *Client) doJSON(t *testing.T, method, path string, body any, out any, opts ...reqOption) (int, error) {
	t.Helper()

	var cfg reqConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	var bodyReader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(t.Context(), method, c.baseURL+path, bodyReader)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("transport: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Capture the body for inclusion in the error message. This is the
		// hook for cyoda's JSON error envelopes (errorCode, message, etc.)
		// — useful for parity-test debugging.
		rawBody, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, fmt.Errorf("%s %s: status %d: %s", method, path, resp.StatusCode, string(rawBody))
	}

	if err := decodeJSONResponse(resp, out); err != nil {
		return resp.StatusCode, err
	}
	return resp.StatusCode, nil
}

// --- Operation methods ---
//
// Each method maps to one cyoda HTTP API operation. The methods are
// added incrementally as parity scenarios need them. Methods that fail
// (non-2xx status, decode error, transport error) call t.Fatalf with
// a clear message including the operation name and the response body
// where applicable.

// doRaw issues an HTTP request with the given method, a raw string body,
// and the standard Content-Type/Authorization headers. Returns the raw
// response body on success (2xx). Returns a descriptive error
// wrapping the response body for non-2xx status codes.
//
// On 409 Conflict with properties.retryable=true (SERIALIZABLE 40001/40P01
// aborts, classified by the server) the request is retried up to 5 times
// with a short backoff — the client's job is to replay against a fresh
// snapshot. Non-retryable 409s (business-logic conflicts) surface
// immediately so tests that assert them can see the first response. This
// is the minimum viable client retry; production clients would use
// bounded jitter and per-operation policies.
func (c *Client) doRaw(t *testing.T, method, path, body string) ([]byte, error) {
	t.Helper()
	return c.doRawWithHeaders(t, method, path, body, nil)
}

// isRetryableConflict reports whether a 409 body advertises
// properties.retryable=true (the server's signal that the transaction
// aborted cleanly and replaying against a fresh snapshot is safe).
func isRetryableConflict(body []byte) bool {
	var problem struct {
		Properties struct {
			Retryable bool `json:"retryable"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(body, &problem); err != nil {
		return false
	}
	return problem.Properties.Retryable
}

// ImportModel issues POST /api/model/import/JSON/SAMPLE_DATA/{name}/{version}
// with the given sample-data document as the body.
func (c *Client) ImportModel(t *testing.T, modelName string, modelVersion int, sampleDoc string) error {
	t.Helper()
	path := fmt.Sprintf("/api/model/import/JSON/SAMPLE_DATA/%s/%d", modelName, modelVersion)
	_, err := c.doRaw(t, http.MethodPost, path, sampleDoc)
	return err
}

// LockModel issues PUT /api/model/{name}/{version}/lock.
func (c *Client) LockModel(t *testing.T, modelName string, modelVersion int) error {
	t.Helper()
	path := fmt.Sprintf("/api/model/%s/%d/lock", modelName, modelVersion)
	_, err := c.doJSON(t, http.MethodPut, path, nil, nil)
	return err
}

// SetChangeLevel issues POST /api/model/{name}/{version}/changeLevel/{level}.
// Levels: STRUCTURAL, TYPE, ARRAY_ELEMENTS, ARRAY_LENGTH (or "" to unset).
func (c *Client) SetChangeLevel(t *testing.T, modelName string, modelVersion int, level string) error {
	t.Helper()
	path := fmt.Sprintf("/api/model/%s/%d/changeLevel/%s", modelName, modelVersion, level)
	_, err := c.doJSON(t, http.MethodPost, path, nil, nil)
	return err
}

// CreateEntityRaw issues POST /api/entity/JSON/{name}/{version} and returns
// the HTTP status code without decoding the body. Used by tests that expect
// non-200 responses (e.g., strict-validate rejections).
func (c *Client) CreateEntityRaw(t *testing.T, modelName string, modelVersion int, body string) (int, []byte, error) {
	t.Helper()
	path := fmt.Sprintf("/api/entity/JSON/%s/%d", modelName, modelVersion)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, c.baseURL+path, strings.NewReader(body))
	if err != nil {
		return 0, nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("transport: %w", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp.StatusCode, raw, nil
}

// ImportWorkflow issues POST /api/model/{name}/{version}/workflow/import
// with the given workflow JSON as the body.
func (c *Client) ImportWorkflow(t *testing.T, modelName string, modelVersion int, workflowJSON string) error {
	t.Helper()
	path := fmt.Sprintf("/api/model/%s/%d/workflow/import", modelName, modelVersion)
	_, err := c.doRaw(t, http.MethodPost, path, workflowJSON)
	return err
}

// CreateEntity issues POST /api/entity/JSON/{name}/{version} with the
// given entity body. Returns the new entity ID as uuid.UUID so callers
// can pass it directly to GetEntity (which also takes uuid.UUID).
func (c *Client) CreateEntity(t *testing.T, modelName string, modelVersion int, body string) (uuid.UUID, error) {
	t.Helper()
	path := fmt.Sprintf("/api/entity/JSON/%s/%d", modelName, modelVersion)
	raw, err := c.doRaw(t, http.MethodPost, path, body)
	if err != nil {
		return uuid.Nil, err
	}
	// The response is an array of EntityTransactionInfo objects, even for
	// a single entity create: [{"transactionId":"...","entityIds":["uuid"]}].
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var txInfos []EntityTransactionInfo
	if err := dec.Decode(&txInfos); err != nil {
		return uuid.Nil, fmt.Errorf("decode CreateEntity response: %w", err)
	}
	if len(txInfos) == 0 {
		return uuid.Nil, fmt.Errorf("CreateEntity returned empty array")
	}
	if len(txInfos[0].EntityIDs) != 1 {
		return uuid.Nil, fmt.Errorf("CreateEntity returned %d ids, want 1", len(txInfos[0].EntityIDs))
	}
	id, err := uuid.Parse(txInfos[0].EntityIDs[0])
	if err != nil {
		return uuid.Nil, fmt.Errorf("parse entity ID %q: %w", txInfos[0].EntityIDs[0], err)
	}
	return id, nil
}

// CreateEntityWithTxID issues POST /api/entity/JSON/{name}/{version} and
// returns both the entity ID and the transactionId from the response.
func (c *Client) CreateEntityWithTxID(t *testing.T, modelName string, modelVersion int, body string) (uuid.UUID, string, error) {
	t.Helper()
	path := fmt.Sprintf("/api/entity/JSON/%s/%d", modelName, modelVersion)
	raw, err := c.doRaw(t, http.MethodPost, path, body)
	if err != nil {
		return uuid.Nil, "", err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var txInfos []EntityTransactionInfo
	if err := dec.Decode(&txInfos); err != nil {
		return uuid.Nil, "", fmt.Errorf("decode CreateEntityWithTxID response: %w", err)
	}
	if len(txInfos) == 0 || len(txInfos[0].EntityIDs) != 1 {
		return uuid.Nil, "", fmt.Errorf("unexpected CreateEntityWithTxID response: %v", txInfos)
	}
	id, err := uuid.Parse(txInfos[0].EntityIDs[0])
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("parse entity ID: %w", err)
	}
	return id, txInfos[0].TransactionID, nil
}

// ListModels issues GET /api/model/ and returns the parsed model list.
// Canonical: docs/cyoda/openapi.yml:2764 (getAvailableEntityModels).
func (c *Client) ListModels(t *testing.T) ([]EntityModelDto, error) {
	t.Helper()
	var models []EntityModelDto
	if _, err := c.doJSON(t, http.MethodGet, "/api/model/", nil, &models); err != nil {
		return nil, err
	}
	return models, nil
}

// ExportModel issues GET /api/model/export/{converter}/{name}/{version}.
// Returns raw JSON. Canonical: docs/cyoda/openapi.yml:2805 (exportMetadata).
func (c *Client) ExportModel(t *testing.T, converter, modelName string, modelVersion int) (json.RawMessage, error) {
	t.Helper()
	path := fmt.Sprintf("/api/model/export/%s/%s/%d", converter, modelName, modelVersion)
	var raw json.RawMessage
	if _, err := c.doJSON(t, http.MethodGet, path, nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// ExportWorkflow issues GET /api/model/{name}/{version}/workflow/export.
// Returns raw JSON. Canonical: docs/cyoda/openapi.yml:3415.
func (c *Client) ExportWorkflow(t *testing.T, modelName string, modelVersion int) (json.RawMessage, error) {
	t.Helper()
	path := fmt.Sprintf("/api/model/%s/%d/workflow/export", modelName, modelVersion)
	var raw json.RawMessage
	if _, err := c.doJSON(t, http.MethodGet, path, nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// UnlockModel issues PUT /api/model/{name}/{version}/unlock.
// Canonical: docs/cyoda/openapi.yml:3338.
func (c *Client) UnlockModel(t *testing.T, modelName string, modelVersion int) error {
	t.Helper()
	path := fmt.Sprintf("/api/model/%s/%d/unlock", modelName, modelVersion)
	_, err := c.doJSON(t, http.MethodPut, path, nil, nil)
	return err
}

// DeleteModel issues DELETE /api/model/{name}/{version}.
// Canonical: docs/cyoda/openapi.yml:3094 (deleteEntityModel).
func (c *Client) DeleteModel(t *testing.T, modelName string, modelVersion int) error {
	t.Helper()
	path := fmt.Sprintf("/api/model/%s/%d", modelName, modelVersion)
	_, err := c.doJSON(t, http.MethodDelete, path, nil, nil)
	return err
}

// GetEntity issues GET /api/entity/{entityId}.
//
// Canonical: docs/cyoda/openapi.yml line 1055 (`getOneEntity`).
// Per approved deviation A1, the response is the {type, data, meta}
// envelope (the parity-local EntityResult type), not bare data as the
// published OpenAPI spec shows. Per approved deviation A2, the meta
// envelope on getOneEntity includes modelKey.
func (c *Client) GetEntity(t *testing.T, entityID uuid.UUID) (EntityResult, error) {
	t.Helper()
	var ent EntityResult
	if _, err := c.doJSON(t, http.MethodGet, "/api/entity/"+entityID.String(), nil, &ent); err != nil {
		return EntityResult{}, err
	}
	return ent, nil
}

// DeleteEntity issues DELETE /api/entity/{entityId}.
// Canonical: docs/cyoda/openapi.yml:1147 (deleteSingleEntity).
func (c *Client) DeleteEntity(t *testing.T, entityID uuid.UUID) error {
	t.Helper()
	path := "/api/entity/" + entityID.String()
	_, err := c.doJSON(t, http.MethodDelete, path, nil, nil)
	return err
}

// GetEntityChanges issues GET /api/entity/{entityId}/changes.
// Returns the change history as []EntityChangeMeta.
// Canonical: docs/cyoda/openapi.yml:1207 (getEntityChangesMetadata).
func (c *Client) GetEntityChanges(t *testing.T, entityID uuid.UUID) ([]EntityChangeMeta, error) {
	t.Helper()
	path := "/api/entity/" + entityID.String() + "/changes"
	var changes []EntityChangeMeta
	if _, err := c.doJSON(t, http.MethodGet, path, nil, &changes); err != nil {
		return nil, err
	}
	return changes, nil
}

// ListEntitiesByModel issues GET /api/entity/{name}/{version}.
// Returns the entity list (each as EntityResult without modelKey per A2).
// Canonical: docs/cyoda/openapi.yml:1326 (getAllEntities).
func (c *Client) ListEntitiesByModel(t *testing.T, modelName string, modelVersion int) ([]EntityResult, error) {
	t.Helper()
	path := fmt.Sprintf("/api/entity/%s/%d", modelName, modelVersion)
	var entities []EntityResult
	if _, err := c.doJSON(t, http.MethodGet, path, nil, &entities); err != nil {
		return nil, err
	}
	return entities, nil
}

// GetEntityAt issues GET /api/entity/{entityId}?pointInTime=<t>.
// Returns the entity as it was at the given point in time.
// Canonical: docs/cyoda/openapi.yml:1055 (getOneEntity with pointInTime query param).
// This is the code path that exercised the GetAsAt bug (PR #173).
func (c *Client) GetEntityAt(t *testing.T, entityID uuid.UUID, pointInTime time.Time) (EntityResult, error) {
	t.Helper()
	path := fmt.Sprintf("/api/entity/%s?pointInTime=%s", entityID.String(), pointInTime.Format(time.RFC3339Nano))
	var ent EntityResult
	if _, err := c.doJSON(t, http.MethodGet, path, nil, &ent); err != nil {
		return EntityResult{}, err
	}
	return ent, nil
}

// GetEntityAtRaw issues GET /api/entity/{entityId}?pointInTime=<t>.
// Returns (statusCode, error) without decoding the body -- for testing
// 404 responses where there is no entity to decode.
func (c *Client) GetEntityAtRaw(t *testing.T, entityID uuid.UUID, pointInTime time.Time) (int, error) {
	t.Helper()
	path := fmt.Sprintf("/api/entity/%s?pointInTime=%s", entityID.String(), pointInTime.Format(time.RFC3339Nano))
	status, err := c.doJSON(t, http.MethodGet, path, nil, nil)
	return status, err
}

// UpdateEntityData issues PUT /api/entity/JSON/{entityId} to update
// entity data without firing a workflow transition.
// Canonical: docs/cyoda/openapi.yml (collection updateOne).
func (c *Client) UpdateEntityData(t *testing.T, entityID uuid.UUID, body string) error {
	t.Helper()
	path := "/api/entity/JSON/" + entityID.String()
	_, err := c.doRaw(t, http.MethodPut, path, body)
	return err
}

// UpdateEntity issues PUT /api/entity/JSON/{entityId}/{transition} with the
// given entity body. Returns an error if the request fails.
// Canonical: docs/cyoda/openapi.yml:2037 (updateOne / transition).
func (c *Client) UpdateEntity(t *testing.T, entityID uuid.UUID, transition, body string) error {
	t.Helper()
	path := fmt.Sprintf("/api/entity/JSON/%s/%s", entityID.String(), transition)
	_, err := c.doRaw(t, http.MethodPut, path, body)
	return err
}

// CollectionItem is one entry in a POST /api/entity/{format} body for
// heterogeneous collection creation. Payload is a JSON-encoded string
// (not a nested object) per the wire contract — the handler in
// internal/domain/entity.Handler.CreateCollection unmarshals it as such.
type CollectionItem struct {
	ModelName    string
	ModelVersion int
	Payload      string
}

// CreateEntitiesCollection issues POST /api/entity/JSON with a
// heterogeneous batch. Returns the list of created entity IDs (parsed
// from the response array's entityIds field).
func (c *Client) CreateEntitiesCollection(t *testing.T, items []CollectionItem) ([]uuid.UUID, error) {
	t.Helper()
	type modelRef struct {
		Name    string `json:"name"`
		Version int    `json:"version"`
	}
	type rawItem struct {
		Model   modelRef `json:"model"`
		Payload string   `json:"payload"`
	}
	raw := make([]rawItem, 0, len(items))
	for _, it := range items {
		raw = append(raw, rawItem{
			Model:   modelRef{Name: it.ModelName, Version: it.ModelVersion},
			Payload: it.Payload,
		})
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshal CreateEntitiesCollection items: %w", err)
	}
	resp, err := c.doRaw(t, http.MethodPost, "/api/entity/JSON", string(body))
	if err != nil {
		return nil, err
	}
	// Response shape: [{"transactionId":"...","entityIds":["<uuid>", ...]}]
	var parsed []EntityTransactionInfo
	if err := json.Unmarshal(resp, &parsed); err != nil {
		return nil, fmt.Errorf("decode CreateEntitiesCollection response: %w (body=%s)", err, string(resp))
	}
	var out []uuid.UUID
	for _, tx := range parsed {
		for _, idStr := range tx.EntityIDs {
			id, perr := uuid.Parse(idStr)
			if perr != nil {
				return nil, fmt.Errorf("parse entityId %q: %w", idStr, perr)
			}
			out = append(out, id)
		}
	}
	return out, nil
}

// UpdateCollectionItem is one entry in a PUT /api/entity/{format} body.
// Payload is a JSON-encoded string (not a nested object) per the collection
// update wire contract.
type UpdateCollectionItem struct {
	ID         uuid.UUID
	Payload    string
	Transition string // optional; "" = loopback
}

// UpdateCollection issues PUT /api/entity/JSON with a batch of
// UpdateCollectionItem. Returns the raw response body on success so
// callers can assert the [{transactionId, entityIds}] shape, or an error
// wrapping the body on non-2xx.
// Canonical: docs/cyoda/openapi.yml (collection update).
func (c *Client) UpdateCollection(t *testing.T, items []UpdateCollectionItem) ([]byte, error) {
	t.Helper()
	type rawItem struct {
		ID         string `json:"id"`
		Payload    string `json:"payload"`
		Transition string `json:"transition,omitempty"`
	}
	raw := make([]rawItem, 0, len(items))
	for _, it := range items {
		raw = append(raw, rawItem{ID: it.ID.String(), Payload: it.Payload, Transition: it.Transition})
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshal UpdateCollection items: %w", err)
	}
	return c.doRaw(t, http.MethodPut, "/api/entity/JSON", string(body))
}

// GetEntityRaw issues GET /api/entity/{entityId} and returns the HTTP
// status code without decoding the body. Used by tests that expect
// non-200 responses (e.g., tenant isolation cross-tenant GET → 404).
func (c *Client) GetEntityRaw(t *testing.T, entityID uuid.UUID) (int, error) {
	t.Helper()
	path := "/api/entity/" + entityID.String()
	return c.doJSON(t, http.MethodGet, path, nil, nil)
}

// DeleteEntityRaw issues DELETE /api/entity/{entityId} and returns the
// HTTP status code without fataling. Used by tests that expect the
// delete to fail (e.g., tenant isolation cross-tenant delete → 404).
func (c *Client) DeleteEntityRaw(t *testing.T, entityID uuid.UUID) (int, error) {
	t.Helper()
	path := "/api/entity/" + entityID.String()
	return c.doJSON(t, http.MethodDelete, path, nil, nil)
}

// GetWorkflowFinished issues GET /api/audit/entity/{entityId}/workflow/{txId}/finished
// and returns the HTTP status code and the decoded JSON response body.
// On non-2xx responses the returned map is nil and the error contains the
// response body for diagnostics.
func (c *Client) GetWorkflowFinished(t *testing.T, entityID uuid.UUID, txID string) (int, map[string]any, error) {
	t.Helper()
	path := fmt.Sprintf("/api/audit/entity/%s/workflow/%s/finished", entityID.String(), txID)
	var result map[string]any
	status, err := c.doJSON(t, http.MethodGet, path, nil, &result)
	if err != nil {
		return status, nil, err
	}
	return status, result, nil
}

// GetAuditEventsRaw issues GET /api/audit/entity/{entityId} and returns
// the HTTP status code without decoding. Used by tests that expect
// non-200 responses (e.g., tenant isolation cross-tenant audit → 404).
func (c *Client) GetAuditEventsRaw(t *testing.T, entityID uuid.UUID) (int, error) {
	t.Helper()
	path := "/api/audit/entity/" + entityID.String()
	return c.doJSON(t, http.MethodGet, path, nil, nil)
}

// MessageHeaderInput collects the optional message-header fields cyoda-go
// reads from HTTP headers on POST /api/message/new/{subject}. Subject is
// in the path, so it is not part of this struct.
//
// Content-Type is sent as the standard HTTP Content-Type header; if the
// caller leaves it empty it defaults to "application/json". Content-Encoding
// is sent as the standard Content-Encoding header. The X-* fields are sent
// as the corresponding cyoda-specific request headers. Empty fields are
// omitted from the request.
//
// Source of truth: api/generated.go NewMessageParams — all fields are
// ParamLocationHeader (Content-Type and Content-Length are required;
// Content-Encoding and X-* are optional).
type MessageHeaderInput struct {
	ContentType     string
	ContentEncoding string
	MessageID       string
	UserID          string
	Recipient       string
	ReplyTo         string
	CorrelationID   string
}

// doRawWithHeaders is like doRaw but accepts caller-supplied HTTP headers.
// Headers in extraHeaders are applied first; the client's Authorization
// header is always set last from c.token, so caller-supplied headers
// CANNOT override Authorization. Content-Type defaults to
// "application/json" when extraHeaders does not contain one.
func (c *Client) doRawWithHeaders(t *testing.T, method, path, body string, extraHeaders http.Header) ([]byte, error) {
	t.Helper()
	const maxAttempts = 5
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(t.Context(), method, c.baseURL+path, strings.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		// Apply caller-supplied headers first; Authorization is overwritten below.
		for k, vs := range extraHeaders {
			req.Header[k] = append([]string(nil), vs...) // defensive copy; replaces any existing
		}
		// Fall back to application/json if the caller didn't specify Content-Type.
		if req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("transport: %w", err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return raw, nil
		}
		if resp.StatusCode == http.StatusConflict && isRetryableConflict(raw) && attempt < maxAttempts-1 {
			time.Sleep(time.Duration(10*(attempt+1)) * time.Millisecond)
			lastErr = fmt.Errorf("%s %s: status 409: %s", method, path, string(raw))
			continue
		}
		return nil, fmt.Errorf("%s %s: status %d: %s", method, path, resp.StatusCode, string(raw))
	}
	return nil, lastErr
}

// CreateMessage issues POST /api/message/new/{subject} with the given
// payload wrapped in the edge-message envelope {payload, meta-data}.
// Returns the message ID.
// Canonical: docs/cyoda/openapi.yml:2401.
func (c *Client) CreateMessage(t *testing.T, subject, payload string) (string, error) {
	t.Helper()
	path := "/api/message/new/" + subject
	body := fmt.Sprintf(`{"payload": %s, "meta-data": {"source": "parity"}}`, payload)
	raw, err := c.doRaw(t, http.MethodPost, path, body)
	if err != nil {
		return "", err
	}
	// Response is an array of EntityTransactionInfo-like objects.
	var results []EntityTransactionInfo
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&results); err != nil {
		return "", fmt.Errorf("decode CreateMessage response: %w", err)
	}
	if len(results) == 0 || len(results[0].EntityIDs) == 0 {
		return "", fmt.Errorf("CreateMessage returned empty entity IDs")
	}
	return results[0].EntityIDs[0], nil
}

// CreateMessageWithHeaders is the header-rich variant of CreateMessage.
// It sends the fields in MessageHeaderInput as HTTP request headers so
// cyoda-go's generated handler reads them via NewMessageParams. The body
// envelope is identical to CreateMessage: {"payload": <payload>,
// "meta-data": {"source": "parity"}}.
//
// If header.ContentType is empty it defaults to "application/json".
// Empty fields in header are omitted from the request.
// Returns the new message ID.
func (c *Client) CreateMessageWithHeaders(t *testing.T, subject, payload string, header MessageHeaderInput) (string, error) {
	t.Helper()
	path := "/api/message/new/" + subject
	body := fmt.Sprintf(`{"payload": %s, "meta-data": {"source": "parity"}}`, payload)

	h := make(http.Header)
	ct := header.ContentType
	if ct == "" {
		ct = "application/json"
	}
	h.Set("Content-Type", ct)
	if header.ContentEncoding != "" {
		h.Set("Content-Encoding", header.ContentEncoding)
	}
	if header.MessageID != "" {
		h.Set("X-Message-ID", header.MessageID)
	}
	if header.UserID != "" {
		h.Set("X-User-ID", header.UserID)
	}
	if header.Recipient != "" {
		h.Set("X-Recipient", header.Recipient)
	}
	if header.ReplyTo != "" {
		h.Set("X-Reply-To", header.ReplyTo)
	}
	if header.CorrelationID != "" {
		h.Set("X-Correlation-ID", header.CorrelationID)
	}

	raw, err := c.doRawWithHeaders(t, http.MethodPost, path, body, h)
	if err != nil {
		return "", err
	}
	var results []EntityTransactionInfo
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&results); err != nil {
		return "", fmt.Errorf("decode CreateMessageWithHeaders response: %w", err)
	}
	if len(results) == 0 || len(results[0].EntityIDs) == 0 {
		return "", fmt.Errorf("CreateMessageWithHeaders returned empty entity IDs")
	}
	return results[0].EntityIDs[0], nil
}

// GetMessage issues GET /api/message/{messageId} and returns the raw
// response body as a map. The response shape is {header, metaData, content}.
// Canonical: docs/cyoda/openapi.yml:2598.
func (c *Client) GetMessage(t *testing.T, messageID string) (map[string]any, error) {
	t.Helper()
	path := "/api/message/" + messageID
	var result map[string]any
	if _, err := c.doJSON(t, http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// DeleteMessage issues DELETE /api/message/{messageId}.
func (c *Client) DeleteMessage(t *testing.T, messageID string) error {
	t.Helper()
	path := "/api/message/" + messageID
	_, err := c.doJSON(t, http.MethodDelete, path, nil, nil)
	return err
}

// GetEntityStatsRaw issues GET /api/entity/stats and returns the raw
// status code. The response shape is backend-specific; we only verify
// it returns 200 (not 500).
func (c *Client) GetEntityStatsRaw(t *testing.T) (int, error) {
	t.Helper()
	return c.doJSON(t, http.MethodGet, "/api/entity/stats", nil, nil)
}

// SyncSearch issues POST /api/search/direct/{name}/{version} with the
// given condition JSON. Returns the entity results.
// The sync search endpoint returns application/x-ndjson. This method
// reads the response line-by-line (NDJSON).
// Canonical: docs/cyoda/api/openapi-entity-search.yml:471 (searchEntities).
func (c *Client) SyncSearch(t *testing.T, modelName string, modelVersion int, condition string) ([]EntityResult, error) {
	t.Helper()
	path := fmt.Sprintf("/api/search/direct/%s/%d", modelName, modelVersion)
	raw, err := c.doRaw(t, http.MethodPost, path, condition)
	if err != nil {
		return nil, err
	}
	// Parse NDJSON: one EntityResult per line.
	var results []EntityResult
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if line == "" {
			continue
		}
		var r EntityResult
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&r); err != nil {
			return nil, fmt.Errorf("decode NDJSON line: %w", err)
		}
		results = append(results, r)
	}
	return results, nil
}

// GetAuditEvents issues GET /api/audit/entity/{entityId} with optional
// query parameters for filtering.
// Canonical: docs/cyoda/api/openapi-audit.yml:31 (SearchEntityAuditEvents).
// Returns the canonical EntityAuditEventsResponse with discriminated-union
// AuditEvent items — use AsStateMachine() / AsEntityChange() / AsSystem()
// to decode specific subtypes.
func (c *Client) GetAuditEvents(t *testing.T, entityID uuid.UUID) (EntityAuditEventsResponse, error) {
	t.Helper()
	path := "/api/audit/entity/" + entityID.String()
	var resp EntityAuditEventsResponse
	if _, err := c.doJSON(t, http.MethodGet, path, nil, &resp); err != nil {
		return EntityAuditEventsResponse{}, err
	}
	return resp, nil
}

// DeleteEntitiesByModel issues DELETE /api/entity/{name}/{version},
// removing all entities in that (name, version) namespace for the
// calling tenant. Returns nil on 2xx; the response body's delete-stats
// shape is not returned because tests typically re-verify via
// ListEntitiesByModel rather than parsing stats.
func (c *Client) DeleteEntitiesByModel(t *testing.T, name string, version int) error {
	t.Helper()
	path := fmt.Sprintf("/api/entity/%s/%d", name, version)
	_, err := c.doRaw(t, http.MethodDelete, path, "")
	return err
}

// DeleteEntitiesByModelAt issues DELETE /api/entity/{name}/{version}?pointInTime=<ISO8601>,
// removing only entities whose creation time is at or before pointInTime
// for the calling tenant. Wraps DeleteEntitiesByModel with a temporal
// filter; everything else is identical.
func (c *Client) DeleteEntitiesByModelAt(t *testing.T, name string, version int, pointInTime time.Time) error {
	t.Helper()
	path := fmt.Sprintf("/api/entity/%s/%d?pointInTime=%s", name, version, pointInTime.UTC().Format(time.RFC3339Nano))
	_, err := c.doRaw(t, http.MethodDelete, path, "")
	return err
}

// LockModelRaw issues PUT /api/model/{name}/{version}/lock and returns
// the HTTP status code + raw body without raising on non-2xx. Used by
// negative-path tests that assert on the error body shape via
// e2e/externalapi/errorcontract.Match. Mirrors the *Raw pattern of
// CreateEntityRaw/GetEntityRaw/DeleteEntityRaw.
func (c *Client) LockModelRaw(t *testing.T, name string, version int) (int, []byte, error) {
	t.Helper()
	path := fmt.Sprintf("/api/model/%s/%d/lock", name, version)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, c.baseURL+path, strings.NewReader(""))
	if err != nil {
		return 0, nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("transport: %w", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp.StatusCode, raw, nil
}

// SetChangeLevelRaw issues POST /api/model/{name}/{version}/changeLevel/{level}
// and returns status+body for negative-path assertions via errorcontract.Match.
func (c *Client) SetChangeLevelRaw(t *testing.T, name string, version int, level string) (int, []byte, error) {
	t.Helper()
	path := fmt.Sprintf("/api/model/%s/%d/changeLevel/%s", name, version, level)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, c.baseURL+path, strings.NewReader(""))
	if err != nil {
		return 0, nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("transport: %w", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp.StatusCode, raw, nil
}

// ImportModelRaw issues POST /api/model/import/JSON/SAMPLE_DATA/{name}/{version}
// with the given sample document as the body, and returns status+body for
// negative-path assertions.
func (c *Client) ImportModelRaw(t *testing.T, name string, version int, sample string) (int, []byte, error) {
	t.Helper()
	path := fmt.Sprintf("/api/model/import/JSON/SAMPLE_DATA/%s/%d", name, version)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, c.baseURL+path, strings.NewReader(sample))
	if err != nil {
		return 0, nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("transport: %w", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp.StatusCode, raw, nil
}

// UpdateEntityRaw issues PUT /api/entity/JSON/{entityId}/{transition} with the
// given body and returns status+body for negative-path assertions.
func (c *Client) UpdateEntityRaw(t *testing.T, id uuid.UUID, transition, body string) (int, []byte, error) {
	t.Helper()
	path := fmt.Sprintf("/api/entity/JSON/%s/%s", id.String(), transition)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, c.baseURL+path, strings.NewReader(body))
	if err != nil {
		return 0, nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("transport: %w", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp.StatusCode, raw, nil
}

// GetEntityChangesRaw issues GET /api/entity/{entityId}/changes and returns
// status+body for negative-path assertions.
func (c *Client) GetEntityChangesRaw(t *testing.T, id uuid.UUID) (int, []byte, error) {
	t.Helper()
	path := fmt.Sprintf("/api/entity/%s/changes", id.String())
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, c.baseURL+path, strings.NewReader(""))
	if err != nil {
		return 0, nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("transport: %w", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp.StatusCode, raw, nil
}

// ImportWorkflowRaw issues POST /api/model/{name}/{version}/workflow/import
// with the given workflow JSON as the body and returns status+body for
// negative-path assertions.
func (c *Client) ImportWorkflowRaw(t *testing.T, name string, version int, body string) (int, []byte, error) {
	t.Helper()
	path := fmt.Sprintf("/api/model/%s/%d/workflow/import", name, version)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, c.baseURL+path, strings.NewReader(body))
	if err != nil {
		return 0, nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("transport: %w", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp.StatusCode, raw, nil
}
