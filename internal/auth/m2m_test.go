package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestM2M_CreateClient(t *testing.T) {
	store := NewInMemoryM2MClientStore()
	handler := NewM2MHandler(store)

	body := `{"tenantId":"tenant-1","userId":"user-1","roles":["ROLE_ADMIN"]}`
	req := httptest.NewRequest(http.MethodPost, "/account/m2m", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", rec.Code)
	}

	var resp m2mClientResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.ClientID == "" {
		t.Fatal("expected non-empty clientId")
	}
	if resp.ClientSecret == "" {
		t.Fatal("expected non-empty clientSecret")
	}
}

func TestM2M_ListClients(t *testing.T) {
	store := NewInMemoryM2MClientStore()
	handler := NewM2MHandler(store)

	// Create a client first.
	body := `{"tenantId":"tenant-1","userId":"user-1","roles":["ROLE_ADMIN","ROLE_USER"]}`
	createReq := httptest.NewRequest(http.MethodPost, "/account/m2m", bytes.NewBufferString(body))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, createReq)

	if createRec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d", createRec.Code)
	}

	var created m2mClientResponse
	json.NewDecoder(createRec.Body).Decode(&created)

	// List clients.
	listReq := httptest.NewRequest(http.MethodGet, "/account/m2m", nil)
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)

	if listRec.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", listRec.Code)
	}

	var clients []m2mClientInfoResponse
	if err := json.NewDecoder(listRec.Body).Decode(&clients); err != nil {
		t.Fatalf("failed to decode list response: %v", err)
	}

	if len(clients) != 1 {
		t.Fatalf("expected 1 client, got %d", len(clients))
	}

	c := clients[0]
	if c.ClientID != created.ClientID {
		t.Errorf("expected clientId %s, got %s", created.ClientID, c.ClientID)
	}
	if c.TenantID != "tenant-1" {
		t.Errorf("expected tenantId tenant-1, got %s", c.TenantID)
	}
	if c.UserID != "user-1" {
		t.Errorf("expected userId user-1, got %s", c.UserID)
	}
	if len(c.Roles) != 2 || c.Roles[0] != "ROLE_ADMIN" || c.Roles[1] != "ROLE_USER" {
		t.Errorf("unexpected roles: %v", c.Roles)
	}
}

func TestM2M_VerifySecret(t *testing.T) {
	store := NewInMemoryM2MClientStore()
	handler := NewM2MHandler(store)

	body := `{"tenantId":"tenant-1","userId":"user-1","roles":["ROLE_ADMIN"]}`
	req := httptest.NewRequest(http.MethodPost, "/account/m2m", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp m2mClientResponse
	json.NewDecoder(rec.Body).Decode(&resp)

	// Verify the returned secret works.
	ok, err := store.VerifySecret(resp.ClientID, resp.ClientSecret)
	if err != nil {
		t.Fatalf("VerifySecret error: %v", err)
	}
	if !ok {
		t.Fatal("expected secret to verify successfully")
	}

	// Verify a wrong secret does not work.
	ok, err = store.VerifySecret(resp.ClientID, "wrong-secret")
	if err != nil {
		t.Fatalf("VerifySecret error: %v", err)
	}
	if ok {
		t.Fatal("expected wrong secret to fail verification")
	}
}

func TestM2M_ResetSecret(t *testing.T) {
	store := NewInMemoryM2MClientStore()
	handler := NewM2MHandler(store)

	// Create a client.
	body := `{"tenantId":"tenant-1","userId":"user-1","roles":["ROLE_ADMIN"]}`
	createReq := httptest.NewRequest(http.MethodPost, "/account/m2m", bytes.NewBufferString(body))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, createReq)

	var created m2mClientResponse
	json.NewDecoder(createRec.Body).Decode(&created)
	oldSecret := created.ClientSecret

	// Reset secret.
	resetReq := httptest.NewRequest(http.MethodPost, "/account/m2m/"+created.ClientID+"/secret/reset", nil)
	resetRec := httptest.NewRecorder()
	handler.ServeHTTP(resetRec, resetReq)

	if resetRec.Code != http.StatusOK {
		t.Fatalf("reset: expected 200, got %d", resetRec.Code)
	}

	var resetResp m2mClientResponse
	json.NewDecoder(resetRec.Body).Decode(&resetResp)

	if resetResp.ClientID != created.ClientID {
		t.Errorf("expected clientId %s, got %s", created.ClientID, resetResp.ClientID)
	}
	if resetResp.ClientSecret == "" {
		t.Fatal("expected non-empty new clientSecret")
	}
	if resetResp.ClientSecret == oldSecret {
		t.Fatal("expected new secret to differ from old secret")
	}

	// New secret should verify.
	ok, err := store.VerifySecret(created.ClientID, resetResp.ClientSecret)
	if err != nil {
		t.Fatalf("VerifySecret error: %v", err)
	}
	if !ok {
		t.Fatal("expected new secret to verify")
	}

	// Old secret should no longer verify.
	ok, err = store.VerifySecret(created.ClientID, oldSecret)
	if err != nil {
		t.Fatalf("VerifySecret error: %v", err)
	}
	if ok {
		t.Fatal("expected old secret to fail verification after reset")
	}
}

func TestM2M_DeleteClient(t *testing.T) {
	store := NewInMemoryM2MClientStore()
	handler := NewM2MHandler(store)

	// Create a client.
	body := `{"tenantId":"tenant-1","userId":"user-1","roles":["ROLE_ADMIN"]}`
	createReq := httptest.NewRequest(http.MethodPost, "/account/m2m", bytes.NewBufferString(body))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, createReq)

	var created m2mClientResponse
	json.NewDecoder(createRec.Body).Decode(&created)

	// Delete the client.
	deleteReq := httptest.NewRequest(http.MethodDelete, "/account/m2m/"+created.ClientID, nil)
	deleteRec := httptest.NewRecorder()
	handler.ServeHTTP(deleteRec, deleteReq)

	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete: expected 204, got %d", deleteRec.Code)
	}

	// List should be empty.
	listReq := httptest.NewRequest(http.MethodGet, "/account/m2m", nil)
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)

	var clients []m2mClientInfoResponse
	json.NewDecoder(listRec.Body).Decode(&clients)

	if len(clients) != 0 {
		t.Fatalf("expected 0 clients after delete, got %d", len(clients))
	}
}

func TestM2M_DeleteNonExistent(t *testing.T) {
	store := NewInMemoryM2MClientStore()
	handler := NewM2MHandler(store)

	req := httptest.NewRequest(http.MethodDelete, "/account/m2m/non-existent-id", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}
