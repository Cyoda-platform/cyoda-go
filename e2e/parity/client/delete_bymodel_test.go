package client_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cyoda-platform/cyoda-go/e2e/parity/client"
)

func TestDeleteEntitiesByModel_DELETE_NoBody(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"deleteResult":{"numberOfEntitites":0,"numberOfEntititesRemoved":0,"idToError":{}},"entityModelClassId":"abc"}]`))
	}))
	defer srv.Close()

	c := client.NewClient(srv.URL, "fake-token")
	if err := c.DeleteEntitiesByModel(t, "family", 1); err != nil {
		t.Fatalf("DeleteEntitiesByModel: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method: got %q, want DELETE", gotMethod)
	}
	if gotPath != "/api/entity/family/1" {
		t.Errorf("path: got %q, want /api/entity/family/1", gotPath)
	}
	if gotBody != "" {
		t.Errorf("body: got %q, want empty", gotBody)
	}
}
