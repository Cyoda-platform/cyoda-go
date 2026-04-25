package driver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cyoda-platform/cyoda-go/e2e/externalapi/driver"
)

// fakeServer accepts any request and returns a generic success body.
// Individual tests assert on method + path only.
func fakeServer(t *testing.T, capture *capturedReq) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.method = r.Method
		capture.path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/entity/JSON") && r.Method == http.MethodPost:
			// Create-entity returns [uuid] on POST /api/entity/JSON/{name}/{version}
			// and [{transactionId, entityIds:[uuid]}] on POST /api/entity/JSON.
			if strings.Count(r.URL.Path, "/") >= 5 {
				_, _ = w.Write([]byte(`[{"transactionId":"tx","entityIds":["00000000-0000-0000-0000-000000000001"]}]`))
			} else {
				_, _ = w.Write([]byte(`[{"transactionId":"tx","entityIds":["00000000-0000-0000-0000-000000000001"]}]`))
			}
		case strings.HasPrefix(r.URL.Path, "/api/model/export"):
			_, _ = w.Write([]byte(`{"$":{".x":"INTEGER"}}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
}

type capturedReq struct{ method, path string }

func TestDriver_CreateModelFromSample_POSTs(t *testing.T) {
	cap := &capturedReq{}
	srv := fakeServer(t, cap)
	defer srv.Close()
	d := driver.NewRemote(t, srv.URL, "tok")
	if err := d.CreateModelFromSample("m", 1, `{"a":1}`); err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap.method != http.MethodPost || cap.path != "/api/model/import/JSON/SAMPLE_DATA/m/1" {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
}

func TestDriver_LockModel_PUT(t *testing.T) {
	cap := &capturedReq{}
	srv := fakeServer(t, cap)
	defer srv.Close()
	d := driver.NewRemote(t, srv.URL, "tok")
	if err := d.LockModel("m", 1); err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap.method != http.MethodPut || cap.path != "/api/model/m/1/lock" {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
}

func TestDriver_UnlockModel_PUT(t *testing.T) {
	cap := &capturedReq{}
	srv := fakeServer(t, cap)
	defer srv.Close()
	d := driver.NewRemote(t, srv.URL, "tok")
	if err := d.UnlockModel("m", 1); err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap.method != http.MethodPut || cap.path != "/api/model/m/1/unlock" {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
}

func TestDriver_DeleteModel_DELETE(t *testing.T) {
	cap := &capturedReq{}
	srv := fakeServer(t, cap)
	defer srv.Close()
	d := driver.NewRemote(t, srv.URL, "tok")
	if err := d.DeleteModel("m", 1); err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap.method != http.MethodDelete || cap.path != "/api/model/m/1" {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
}

func TestDriver_ExportModel_GET(t *testing.T) {
	cap := &capturedReq{}
	srv := fakeServer(t, cap)
	defer srv.Close()
	d := driver.NewRemote(t, srv.URL, "tok")
	raw, err := d.ExportModel("SIMPLE_VIEW", "m", 1)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap.method != http.MethodGet || cap.path != "/api/model/export/SIMPLE_VIEW/m/1" {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
	if len(raw) == 0 {
		t.Fatal("expected non-empty raw export JSON")
	}
}

func TestDriver_CreateEntity_POST(t *testing.T) {
	cap := &capturedReq{}
	srv := fakeServer(t, cap)
	defer srv.Close()
	d := driver.NewRemote(t, srv.URL, "tok")
	id, err := d.CreateEntity("m", 1, `{"a":1}`)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap.method != http.MethodPost || cap.path != "/api/entity/JSON/m/1" {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
	if id.String() == "00000000-0000-0000-0000-000000000000" {
		t.Error("expected non-zero uuid")
	}
}

func TestDriver_DeleteEntity_DELETE(t *testing.T) {
	cap := &capturedReq{}
	srv := fakeServer(t, cap)
	defer srv.Close()
	d := driver.NewRemote(t, srv.URL, "tok")
	if err := d.DeleteEntityByIDString("00000000-0000-0000-0000-000000000001"); err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap.method != http.MethodDelete || !strings.HasPrefix(cap.path, "/api/entity/") {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
}

func TestDriver_DeleteEntitiesByModel_DELETE(t *testing.T) {
	cap := &capturedReq{}
	srv := fakeServer(t, cap)
	defer srv.Close()
	d := driver.NewRemote(t, srv.URL, "tok")
	if err := d.DeleteEntitiesByModel("m", 1); err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap.method != http.MethodDelete || cap.path != "/api/entity/m/1" {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
}

func TestDriver_LockModelRaw_PUT_ReturnsStatus(t *testing.T) {
	cap := &capturedReq{}
	srv := fakeServer(t, cap)
	defer srv.Close()
	d := driver.NewRemote(t, srv.URL, "tok")
	status, body, err := d.LockModelRaw("m", 1)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap.method != http.MethodPut || cap.path != "/api/model/m/1/lock" {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
	if status != 200 || len(body) == 0 {
		t.Errorf("expected (200, non-empty), got (%d, %dB)", status, len(body))
	}
}
