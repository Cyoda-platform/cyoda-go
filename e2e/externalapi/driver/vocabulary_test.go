package driver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

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

func TestDriver_DeleteEntitiesByModelAt_DELETE_PointInTime(t *testing.T) {
	cap := &capturedReq{}
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.method = r.Method
		cap.path = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	d := driver.NewRemote(t, srv.URL, "tok")
	if err := d.DeleteEntitiesByModelAt("m", 1, time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap.method != http.MethodDelete || cap.path != "/api/entity/m/1" {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
	if !strings.Contains(gotQuery, "pointInTime=") {
		t.Errorf("missing pointInTime in query: %q", gotQuery)
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

func TestDriver_SetChangeLevel_POST(t *testing.T) {
	cap := &capturedReq{}
	srv := fakeServer(t, cap)
	defer srv.Close()
	d := driver.NewRemote(t, srv.URL, "tok")
	if err := d.SetChangeLevel("m", 1, "STRUCTURAL"); err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap.method != http.MethodPost || cap.path != "/api/model/m/1/changeLevel/STRUCTURAL" {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
}

func TestDriver_UpdateEntity_PUT_WithTransition(t *testing.T) {
	cap := &capturedReq{}
	srv := fakeServer(t, cap)
	defer srv.Close()
	d := driver.NewRemote(t, srv.URL, "tok")
	id := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	if err := d.UpdateEntity(id, "UPDATE", `{"k":2}`); err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap.method != http.MethodPut {
		t.Errorf("method: got %q", cap.method)
	}
	if !strings.Contains(cap.path, "/api/entity/JSON/") || !strings.Contains(cap.path, "/UPDATE") {
		t.Errorf("path: got %q", cap.path)
	}
}

func TestDriver_UpdateEntityData_PUT_Loopback(t *testing.T) {
	cap := &capturedReq{}
	srv := fakeServer(t, cap)
	defer srv.Close()
	d := driver.NewRemote(t, srv.URL, "tok")
	id := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	if err := d.UpdateEntityData(id, `{"k":2}`); err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap.method != http.MethodPut {
		t.Errorf("method: got %q", cap.method)
	}
	if !strings.Contains(cap.path, "/api/entity/JSON/") {
		t.Errorf("path: got %q", cap.path)
	}
}

func TestDriver_GetEntityAt_GET_PointInTimeQuery(t *testing.T) {
	cap := &capturedReq{}
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.method = r.Method
		cap.path = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"type":"ENTITY","data":{},"meta":{"id":"00000000-0000-0000-0000-000000000001","state":"ACTIVE","creationDate":"2026-04-25T00:00:00Z","lastUpdateTime":"2026-04-25T00:00:00Z"}}`))
	}))
	defer srv.Close()
	d := driver.NewRemote(t, srv.URL, "tok")
	pit := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	id := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	if _, err := d.GetEntityAt(id, pit); err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap.method != http.MethodGet {
		t.Errorf("method: got %q", cap.method)
	}
	if !strings.Contains(gotQuery, "pointInTime=") {
		t.Errorf("query missing pointInTime: %q", gotQuery)
	}
}

func TestDriver_GetEntityChanges_GET(t *testing.T) {
	cap := &capturedReq{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.method = r.Method
		cap.path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	d := driver.NewRemote(t, srv.URL, "tok")
	id := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	if _, err := d.GetEntityChanges(id); err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap.method != http.MethodGet || !strings.HasSuffix(cap.path, "/changes") {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
}

func TestDriver_SetChangeLevelRaw(t *testing.T) {
	cap := &capturedReq{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.method = r.Method
		cap.path = r.URL.Path
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	d := driver.NewRemote(t, srv.URL, "tok")
	status, _, err := d.SetChangeLevelRaw("m", 1, "wrong")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap.method != http.MethodPost || cap.path != "/api/model/m/1/changeLevel/wrong" {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
	if status != http.StatusBadRequest {
		t.Errorf("status: got %d", status)
	}
}

func TestDriver_ImportModelRaw(t *testing.T) {
	cap := &capturedReq{}
	srv := fakeServer(t, cap)
	defer srv.Close()
	d := driver.NewRemote(t, srv.URL, "tok")
	if _, _, err := d.ImportModelRaw("m", 1, `{"a":1}`); err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap.method != http.MethodPost || cap.path != "/api/model/import/JSON/SAMPLE_DATA/m/1" {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
}

func TestDriver_UpdateEntityRaw(t *testing.T) {
	cap := &capturedReq{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.method = r.Method
		cap.path = r.URL.Path
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	d := driver.NewRemote(t, srv.URL, "tok")
	id := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	if _, _, err := d.UpdateEntityRaw(id, "BadTransition", `{"k":1}`); err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap.method != http.MethodPut {
		t.Errorf("method: got %q", cap.method)
	}
}

func TestDriver_GetEntityChangesRaw(t *testing.T) {
	cap := &capturedReq{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.method = r.Method
		cap.path = r.URL.Path
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	d := driver.NewRemote(t, srv.URL, "tok")
	id := uuid.New()
	if _, _, err := d.GetEntityChangesRaw(id); err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap.method != http.MethodGet {
		t.Errorf("method: got %q", cap.method)
	}
}

func TestDriver_ImportWorkflowRaw(t *testing.T) {
	cap := &capturedReq{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.method = r.Method
		cap.path = r.URL.Path
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	d := driver.NewRemote(t, srv.URL, "tok")
	if _, _, err := d.ImportWorkflowRaw("m", 1, `{}`); err != nil {
		t.Fatalf("err: %v", err)
	}
	if cap.method != http.MethodPost || cap.path != "/api/model/m/1/workflow/import" {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
}
