package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// A successful write response means the write is visible to every
// subsequent read. The former consistency-wait flag is retired; a request
// still carrying it — with any value — is accepted and the flag ignored.
// One case per write operation × value.
func TestWriteVisibility_RetiredFlagIgnored_AllWriteOps(t *testing.T) {
	const model = "e2e-wvis-flag"
	setupModelWithWorkflow(t, model, `{
		"importMode": "REPLACE",
		"workflows": [{
			"version": "1.1", "name": "wvis-wf", "initialState": "NONE", "active": true,
			"states": {
				"NONE":    {"transitions": [{"name": "init", "next": "CREATED", "manual": false}]},
				"CREATED": {"transitions": [{"name": "touch", "next": "CREATED", "manual": true}]}
			}
		}]
	}`)

	for _, val := range []string{"true", "false", "maybe"} {
		q := "?waitForConsistencyAfter=" + val

		t.Run("create/"+val, func(t *testing.T) {
			resp := doAuth(t, http.MethodPost, fmt.Sprintf("/api/entity/JSON/%s/1%s", model, q), `{"name":"A","amount":1,"status":"new"}`)
			id := firstEntityID(t, resp)
			assertEntityAmount(t, id, 1)
		})
		t.Run("createCollection/"+val, func(t *testing.T) {
			resp := doAuth(t, http.MethodPost, "/api/entity/JSON"+q,
				fmt.Sprintf(`[{"model":{"name":%q,"version":1},"payload":"{\"name\":\"B\",\"amount\":2,\"status\":\"new\"}"}]`, model))
			id := firstEntityID(t, resp)
			assertEntityAmount(t, id, 2)
		})
		t.Run("updateSingle/"+val, func(t *testing.T) {
			id := createEntityE2E(t, model, 1, `{"name":"C","amount":3,"status":"new"}`)
			resp := doAuth(t, http.MethodPut, fmt.Sprintf("/api/entity/JSON/%s/touch%s", id, q), `{"name":"C","amount":30,"status":"new"}`)
			expect200(t, resp)
			assertEntityAmount(t, id, 30)
		})
		t.Run("updateSingleWithLoopback/"+val, func(t *testing.T) {
			id := createEntityE2E(t, model, 1, `{"name":"D","amount":4,"status":"new"}`)
			resp := doAuth(t, http.MethodPut, fmt.Sprintf("/api/entity/JSON/%s%s", id, q), `{"name":"D","amount":40,"status":"new"}`)
			expect200(t, resp)
			assertEntityAmount(t, id, 40)
		})
		t.Run("updateCollection/"+val, func(t *testing.T) {
			id := createEntityE2E(t, model, 1, `{"name":"E","amount":5,"status":"new"}`)
			resp := doAuth(t, http.MethodPut, "/api/entity/JSON"+q,
				fmt.Sprintf(`[{"id":%q,"payload":"{\"name\":\"E\",\"amount\":50,\"status\":\"new\"}"}]`, id))
			expect200(t, resp)
			assertEntityAmount(t, id, 50)
		})
		t.Run("patchSingleWithLoopback/"+val, func(t *testing.T) {
			id, txID := createEntityE2EWithTxID(t, model, 1, `{"name":"F","amount":6,"status":"new"}`)
			resp := patchEntity(t, fmt.Sprintf("/api/entity/JSON/%s%s", id, q), "application/merge-patch+json", txID, `{"amount":60}`)
			expect200(t, resp)
			assertEntityAmount(t, id, 60)
		})
		t.Run("patchSingle/"+val, func(t *testing.T) {
			id, txID := createEntityE2EWithTxID(t, model, 1, `{"name":"G","amount":7,"status":"new"}`)
			resp := patchEntity(t, fmt.Sprintf("/api/entity/JSON/%s/touch%s", id, q), "application/merge-patch+json", txID, `{"amount":70}`)
			expect200(t, resp)
			assertEntityAmount(t, id, 70)
		})
	}
}

func expect200(t *testing.T, resp *http.Response) {
	t.Helper()
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, body)
	}
}

// firstEntityID reads a create response ([{transactionId, entityIds}]) and
// returns its first entity id, failing on any other status.
func firstEntityID(t *testing.T, resp *http.Response) string {
	t.Helper()
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, body)
	}
	var out []struct {
		EntityIDs []string `json:"entityIds"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil || len(out) == 0 || len(out[0].EntityIDs) == 0 {
		t.Fatalf("unexpected create response: %v: %s", err, body)
	}
	return out[0].EntityIDs[0]
}

// assertEntityAmount is the read-your-write half of the contract: the GET
// issued immediately after the write's response sees the written value.
func assertEntityAmount(t *testing.T, id string, want float64) {
	t.Helper()
	data := getEntityData(t, id, "")
	if got := data["amount"]; got != want {
		t.Fatalf("GET after write: amount = %v, want %v", got, want)
	}
}
