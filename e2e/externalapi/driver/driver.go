// Package driver provides the HTTPDriver abstraction used by
// e2e/parity/externalapi scenarios. It has two constructors:
//
//   - NewInProcess(t, fixture) — wraps a parity BackendFixture, minting
//     a fresh tenant per driver. Used by parity Run* tests.
//   - NewRemote(t, baseURL, jwtToken) — takes an arbitrary base URL and
//     pre-minted JWT. Used by the remote-mode smoke test and (later)
//     live cyoda-cloud runs.
//
// Both constructors return the same *Driver type; test code is identical
// regardless of provenance. This is what makes "point it at cyoda-cloud"
// trivial.
package driver

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/cyoda-platform/cyoda-go/e2e/parity"
	parityclient "github.com/cyoda-platform/cyoda-go/e2e/parity/client"
)

// Driver drives cyoda's HTTP API through the dictionary vocabulary.
type Driver struct {
	t      *testing.T
	client *parityclient.Client
}

// NewInProcess wires up a driver against a parity BackendFixture,
// minting one fresh tenant via fixture.NewTenant(t). The tenant's JWT
// is used as the Authorization bearer for every call.
func NewInProcess(t *testing.T, fixture parity.BackendFixture) *Driver {
	t.Helper()
	tenant := fixture.NewTenant(t)
	return &Driver{
		t:      t,
		client: parityclient.NewClient(fixture.BaseURL(), tenant.Token),
	}
}

// NewRemote wires up a driver against an arbitrary base URL using the
// provided JWT. No tenant is minted — the caller is responsible for the
// JWT's tenant identity.
func NewRemote(t *testing.T, baseURL, jwtToken string) *Driver {
	t.Helper()
	return &Driver{
		t:      t,
		client: parityclient.NewClient(baseURL, jwtToken),
	}
}

// ListModelsDiscard lists models and discards the result. It exists only
// to give the driver_test suite a trivial round-trip for wiring checks.
// (Real dictionary helpers follow — create_model_from_sample, etc.)
func (d *Driver) ListModelsDiscard() error {
	_, err := d.client.ListModels(d.t)
	return err
}

// --- Model lifecycle ---

// CreateModelFromSample issues POST /api/model/import/JSON/SAMPLE_DATA/{name}/{version}.
// YAML action: create_model_from_sample.
func (d *Driver) CreateModelFromSample(name string, version int, sample string) error {
	return d.client.ImportModel(d.t, name, version, sample)
}

// UpdateModelFromSample issues POST /api/model/import/JSON/SAMPLE_DATA/{name}/{version}
// against an existing (unlocked) model — same endpoint, upsert semantics.
// YAML action: update_model_from_sample.
func (d *Driver) UpdateModelFromSample(name string, version int, sample string) error {
	return d.client.ImportModel(d.t, name, version, sample)
}

// LockModel issues PUT /api/model/{name}/{version}/lock.
func (d *Driver) LockModel(name string, version int) error {
	return d.client.LockModel(d.t, name, version)
}

// UnlockModel issues PUT /api/model/{name}/{version}/unlock.
func (d *Driver) UnlockModel(name string, version int) error {
	return d.client.UnlockModel(d.t, name, version)
}

// DeleteModel issues DELETE /api/model/{name}/{version}.
func (d *Driver) DeleteModel(name string, version int) error {
	return d.client.DeleteModel(d.t, name, version)
}

// ExportModel issues GET /api/model/export/{converter}/{name}/{version}.
// Returns the raw JSON body.
func (d *Driver) ExportModel(converter, name string, version int) (json.RawMessage, error) {
	return d.client.ExportModel(d.t, converter, name, version)
}

// ListModels issues GET /api/model/.
func (d *Driver) ListModels() ([]parityclient.EntityModelDto, error) {
	return d.client.ListModels(d.t)
}

// --- Entity CRUD ---

// CreateEntity issues POST /api/entity/JSON/{name}/{version}. Returns the
// first entity ID produced.
func (d *Driver) CreateEntity(name string, version int, body string) (uuid.UUID, error) {
	return d.client.CreateEntity(d.t, name, version, body)
}

// CreateEntityRaw issues the same POST but returns the status code + raw
// body for negative-path tests.
func (d *Driver) CreateEntityRaw(name string, version int, body string) (int, []byte, error) {
	return d.client.CreateEntityRaw(d.t, name, version, body)
}

// CreateEntitiesCollection issues POST /api/entity/JSON with a
// heterogeneous body.
func (d *Driver) CreateEntitiesCollection(items []CollectionItem) ([]uuid.UUID, error) {
	converted := make([]parityclient.CollectionItem, 0, len(items))
	for _, it := range items {
		converted = append(converted, parityclient.CollectionItem{
			ModelName: it.ModelName, ModelVersion: it.ModelVersion, Payload: it.Payload,
		})
	}
	return d.client.CreateEntitiesCollection(d.t, converted)
}

// UpdateEntitiesCollection issues PUT /api/entity/JSON with a
// {id, payload, transition} batch. Returns the raw response body.
func (d *Driver) UpdateEntitiesCollection(items []UpdateCollectionItem) ([]byte, error) {
	converted := make([]parityclient.UpdateCollectionItem, 0, len(items))
	for _, it := range items {
		converted = append(converted, parityclient.UpdateCollectionItem{
			ID: it.ID, Payload: it.Payload, Transition: it.Transition,
		})
	}
	return d.client.UpdateCollection(d.t, converted)
}

// DeleteEntity issues DELETE /api/entity/{id}.
func (d *Driver) DeleteEntity(id uuid.UUID) error {
	return d.client.DeleteEntity(d.t, id)
}

// DeleteEntityByIDString is a convenience for test code that holds IDs
// as strings (e.g., echoed from a prior capture). It parses then delegates.
func (d *Driver) DeleteEntityByIDString(idStr string) error {
	id, err := uuid.Parse(idStr)
	if err != nil {
		return err
	}
	return d.client.DeleteEntity(d.t, id)
}

// DeleteEntitiesByModel issues DELETE /api/entity/{name}/{version}.
func (d *Driver) DeleteEntitiesByModel(name string, version int) error {
	return d.client.DeleteEntitiesByModel(d.t, name, version)
}

// DeleteEntitiesByModelAt issues DELETE /api/entity/{name}/{version} with
// pointInTime, removing only entities created at or before that timestamp.
func (d *Driver) DeleteEntitiesByModelAt(name string, version int, pointInTime time.Time) error {
	return d.client.DeleteEntitiesByModelAt(d.t, name, version, pointInTime)
}

// LockModelRaw issues PUT /api/model/{name}/{version}/lock and returns
// the HTTP status + raw body for negative-path assertions via
// errorcontract.Match.
func (d *Driver) LockModelRaw(name string, version int) (int, []byte, error) {
	return d.client.LockModelRaw(d.t, name, version)
}

// SetChangeLevelRaw issues POST /api/model/{name}/{version}/changeLevel/{level}
// with raw response for negative-path assertions.
func (d *Driver) SetChangeLevelRaw(name string, version int, level string) (int, []byte, error) {
	return d.client.SetChangeLevelRaw(d.t, name, version, level)
}

// ImportModelRaw issues the import-from-sample endpoint with raw response
// for negative-path assertions.
func (d *Driver) ImportModelRaw(name string, version int, sample string) (int, []byte, error) {
	return d.client.ImportModelRaw(d.t, name, version, sample)
}

// UpdateEntityRaw issues PUT /api/entity/JSON/{id}/{transition} with raw
// response for negative-path assertions.
func (d *Driver) UpdateEntityRaw(id uuid.UUID, transition, body string) (int, []byte, error) {
	return d.client.UpdateEntityRaw(d.t, id, transition, body)
}

// GetEntityChangesRaw issues GET /api/entity/{id}/changes with raw response
// for negative-path assertions.
func (d *Driver) GetEntityChangesRaw(id uuid.UUID) (int, []byte, error) {
	return d.client.GetEntityChangesRaw(d.t, id)
}

// ImportWorkflowRaw issues POST /api/model/{name}/{version}/workflow/import
// with raw response for negative-path assertions.
func (d *Driver) ImportWorkflowRaw(name string, version int, body string) (int, []byte, error) {
	return d.client.ImportWorkflowRaw(d.t, name, version, body)
}

// ImportWorkflow issues POST /api/model/{name}/{version}/workflow/import.
// YAML action: import_workflow.
func (d *Driver) ImportWorkflow(name string, version int, body string) error {
	return d.client.ImportWorkflow(d.t, name, version, body)
}

// ExportWorkflow issues GET /api/model/{name}/{version}/workflow/export.
// Returns the raw JSON body. YAML action: export_workflow.
func (d *Driver) ExportWorkflow(name string, version int) (json.RawMessage, error) {
	return d.client.ExportWorkflow(d.t, name, version)
}

// GetEntity issues GET /api/entity/{id}.
func (d *Driver) GetEntity(id uuid.UUID) (parityclient.EntityResult, error) {
	return d.client.GetEntity(d.t, id)
}

// ListEntitiesByModel issues GET /api/entity/{name}/{version}.
func (d *Driver) ListEntitiesByModel(name string, version int) ([]parityclient.EntityResult, error) {
	return d.client.ListEntitiesByModel(d.t, name, version)
}

// SetChangeLevel issues POST /api/model/{name}/{version}/changeLevel/{level}.
// YAML action: set_change_level. Valid levels: ARRAY_LENGTH, ARRAY_ELEMENTS,
// TYPE, STRUCTURAL.
func (d *Driver) SetChangeLevel(name string, version int, level string) error {
	return d.client.SetChangeLevel(d.t, name, version, level)
}

// UpdateEntity issues PUT /api/entity/JSON/{entityId}/{transition}.
// YAML action: update_entity_transition.
func (d *Driver) UpdateEntity(id uuid.UUID, transition, body string) error {
	return d.client.UpdateEntity(d.t, id, transition, body)
}

// UpdateEntityData issues PUT /api/entity/JSON/{entityId} (no transition;
// loopback). YAML action: update_entity_loopback.
func (d *Driver) UpdateEntityData(id uuid.UUID, body string) error {
	return d.client.UpdateEntityData(d.t, id, body)
}

// GetEntityAt issues GET /api/entity/{entityId}?pointInTime=<ISO8601>.
// YAML action: get_entity (with pointInTime).
func (d *Driver) GetEntityAt(id uuid.UUID, pointInTime time.Time) (parityclient.EntityResult, error) {
	return d.client.GetEntityAt(d.t, id, pointInTime)
}

// GetEntityChanges issues GET /api/entity/{entityId}/changes.
// YAML action: get_entity_changes.
func (d *Driver) GetEntityChanges(id uuid.UUID) ([]parityclient.EntityChangeMeta, error) {
	return d.client.GetEntityChanges(d.t, id)
}

// --- Edge-message helpers ---

// CreateMessage issues POST /api/message/new/{subject} with a JSON
// payload body. Returns the message ID. YAML action: save_edge_message.
func (d *Driver) CreateMessage(subject, payload string) (string, error) {
	return d.client.CreateMessage(d.t, subject, payload)
}

// CreateMessageWithHeaders is the header-rich variant of CreateMessage.
// See parityclient.MessageHeaderInput for the supported header fields.
func (d *Driver) CreateMessageWithHeaders(subject, payload string, header parityclient.MessageHeaderInput) (string, error) {
	return d.client.CreateMessageWithHeaders(d.t, subject, payload, header)
}

// GetMessage issues GET /api/message/{id}. Returns the full message
// envelope as a map. YAML action: get_edge_message.
func (d *Driver) GetMessage(id string) (map[string]any, error) {
	return d.client.GetMessage(d.t, id)
}

// DeleteMessage issues DELETE /api/message/{id}. YAML action:
// delete_edge_message.
func (d *Driver) DeleteMessage(id string) error {
	return d.client.DeleteMessage(d.t, id)
}

// DeleteMessages issues DELETE /api/message with a batch ID body.
// Returns the deleted-IDs list. YAML action: delete_edge_messages.
func (d *Driver) DeleteMessages(ids []string) ([]string, error) {
	return d.client.DeleteMessages(d.t, ids)
}

// --- Type re-exports for test-side ergonomics ---

// CollectionItem mirrors parityclient.CollectionItem so external callers
// don't need to import the parity/client package directly.
type CollectionItem struct {
	ModelName    string
	ModelVersion int
	Payload      string
}

// UpdateCollectionItem mirrors parityclient.UpdateCollectionItem for the
// same reason.
type UpdateCollectionItem struct {
	ID         uuid.UUID
	Payload    string
	Transition string
}
