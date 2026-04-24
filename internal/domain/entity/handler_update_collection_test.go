package entity_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// Regression test for issue #92. PUT /api/entity/{format} (collection
// update) was a stub returning 501 with a wrong errorCode. The endpoint
// is in the route table and advertised — AI clients hit it and failed.

func doUpdateCollection(t *testing.T, base, format, body string) *http.Response {
	t.Helper()
	url := base + "/entity/" + format
	req, err := http.NewRequest(http.MethodPut, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build update collection request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("update collection request failed: %v", err)
	}
	return resp
}

// TestUpdateCollection_HappyPath — bulk-update two entities; verify
// response shape matches the documented [{transactionId, entityIds}]
// EntityTransactionResponse array.
func TestUpdateCollection_HappyPath(t *testing.T) {
	srv := newTestServer(t)

	importAndLockModel(t, srv.URL, "UpdBatch", 1, `{"name":"x","v":0}`)

	// Seed two entities.
	id1 := doCreateAndGetID(t, srv.URL, "UpdBatch", 1, `{"name":"Alice","v":1}`)
	id2 := doCreateAndGetID(t, srv.URL, "UpdBatch", 1, `{"name":"Bob","v":2}`)

	body := fmt.Sprintf(`[
		{"id":"%s","payload":"{\"name\":\"Alice2\",\"v\":11}"},
		{"id":"%s","payload":"{\"name\":\"Bob2\",\"v\":22}"}
	]`, id1, id2)

	resp := doUpdateCollection(t, srv.URL, "JSON", body)
	defer resp.Body.Close()
	respBody := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, respBody)
	}

	var arr []map[string]any
	if err := json.Unmarshal(respBody, &arr); err != nil {
		t.Fatalf("parse response: %v; body: %s", err, respBody)
	}
	if len(arr) != 1 {
		t.Fatalf("expected single-element EntityTransactionResponse array, got %d", len(arr))
	}
	txID, _ := arr[0]["transactionId"].(string)
	if txID == "" {
		t.Errorf("missing transactionId; body: %s", respBody)
	}
	ids, _ := arr[0]["entityIds"].([]any)
	if len(ids) != 2 {
		t.Fatalf("expected 2 entityIds, got %d: %s", len(ids), respBody)
	}

	// Fetch each back and verify the update landed.
	for _, want := range []struct {
		id   string
		name string
	}{{id1, "Alice2"}, {id2, "Bob2"}} {
		getResp := doGetEntity(t, srv.URL, want.id)
		expectStatus(t, getResp, http.StatusOK)
		gb := readBody(t, getResp)
		if !strings.Contains(string(gb), want.name) {
			t.Errorf("entity %s did not receive update %q; body: %s", want.id, want.name, gb)
		}
	}
}

// TestUpdateCollection_AnyMissingRollsBackAll — per docs "If any entity
// in the collection is not found, the entire operation fails and no
// entities are updated." A valid entity + one bogus UUID must leave the
// valid entity unchanged.
func TestUpdateCollection_AnyMissingRollsBackAll(t *testing.T) {
	srv := newTestServer(t)

	importAndLockModel(t, srv.URL, "UpdBatchRB", 1, `{"name":"x","v":0}`)

	id1 := doCreateAndGetID(t, srv.URL, "UpdBatchRB", 1, `{"name":"Alice","v":1}`)
	bogus := "00000000-0000-0000-0000-000000000000"

	body := fmt.Sprintf(`[
		{"id":"%s","payload":"{\"name\":\"AliceShouldNotLand\",\"v\":999}"},
		{"id":"%s","payload":"{\"name\":\"never\",\"v\":0}"}
	]`, id1, bogus)

	resp := doUpdateCollection(t, srv.URL, "JSON", body)
	defer resp.Body.Close()
	rbody := readBody(t, resp)

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (missing item); body: %s", resp.StatusCode, rbody)
	}

	// Valid entity must be unchanged.
	getResp := doGetEntity(t, srv.URL, id1)
	expectStatus(t, getResp, http.StatusOK)
	gb := readBody(t, getResp)
	if strings.Contains(string(gb), "AliceShouldNotLand") {
		t.Errorf("rollback violation: entity was modified despite a missing sibling; body: %s", gb)
	}
}

// TestUpdateCollection_PayloadMustBeString — per docs contract: payload
// must be a JSON-encoded string, not an object. An object payload is
// rejected with 400 BAD_REQUEST (matches CreateCollection's contract).
func TestUpdateCollection_PayloadMustBeString(t *testing.T) {
	srv := newTestServer(t)

	importAndLockModel(t, srv.URL, "UpdBatchStr", 1, `{"name":"x"}`)
	id1 := doCreateAndGetID(t, srv.URL, "UpdBatchStr", 1, `{"name":"Alice"}`)

	body := fmt.Sprintf(`[
		{"id":"%s","payload":{"name":"bogus"}}
	]`, id1)

	resp := doUpdateCollection(t, srv.URL, "JSON", body)
	defer resp.Body.Close()
	rbody := readBody(t, resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for object payload; body: %s", resp.StatusCode, rbody)
	}
}

// doCreateAndGetID is a small helper used by the UpdateCollection tests:
// create one entity in a 1-element batch and return its UUID.
func doCreateAndGetID(t *testing.T, base, entityName string, version int, payload string) string {
	t.Helper()
	body := `[{"model":{"name":"` + entityName + `","version":` + fmt.Sprintf("%d", version) + `},"payload":` + strconv.Quote(payload) + `}]`
	resp := doCreateCollection(t, base, "JSON", body)
	defer resp.Body.Close()
	expectStatus(t, resp, http.StatusOK)
	rb := readBody(t, resp)
	var arr []map[string]any
	if err := json.Unmarshal(rb, &arr); err != nil {
		t.Fatalf("parse create resp: %v; body: %s", err, rb)
	}
	ids, _ := arr[0]["entityIds"].([]any)
	if len(ids) == 0 {
		t.Fatalf("no entity ids in: %s", rb)
	}
	id, _ := ids[0].(string)
	return id
}
