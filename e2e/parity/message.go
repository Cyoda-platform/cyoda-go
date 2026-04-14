package parity

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cyoda-platform/cyoda-go/e2e/parity/client"
)

// RunMessageCreateAndGet verifies that a message can be created via
// POST /api/message/new/{subject} and then retrieved by ID via
// GET /api/message/{messageId}. Asserts that the response contains
// header.subject matching the creation subject and content containing
// the original payload data.
func RunMessageCreateAndGet(t *testing.T, fixture BackendFixture) {
	tenant := fixture.NewTenant(t)
	c := client.NewClient(fixture.BaseURL(), tenant.Token)

	subject := "parity.test"
	payload := `{"event": "test", "value": 42}`

	// 1. Create message.
	msgID, err := c.CreateMessage(t, subject, payload)
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	// 2. GET the message by ID.
	got, err := c.GetMessage(t, msgID)
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}

	// 3. Verify header.subject.
	header, ok := got["header"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'header' object in response, got: %T", got["header"])
	}
	if gotSubject, _ := header["subject"].(string); gotSubject != subject {
		t.Errorf("header.subject = %q, want %q", gotSubject, subject)
	}

	// 4. Verify content contains payload data.
	content, ok := got["content"].(string)
	if !ok {
		t.Fatalf("expected 'content' string in response, got: %T", got["content"])
	}
	if !strings.Contains(content, `"event"`) || !strings.Contains(content, `"test"`) {
		t.Errorf("content does not contain expected payload data: %s", content)
	}
}

// RunMessageDelete verifies that a deleted message returns an error
// (404) on subsequent GET.
func RunMessageDelete(t *testing.T, fixture BackendFixture) {
	tenant := fixture.NewTenant(t)
	c := client.NewClient(fixture.BaseURL(), tenant.Token)

	// 1. Create message.
	msgID, err := c.CreateMessage(t, "parity.delete", `{"event": "delete-me"}`)
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	// 2. Delete it.
	if err := c.DeleteMessage(t, msgID); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}

	// 3. GET returns error (404).
	_, err = c.GetMessage(t, msgID)
	if err == nil {
		t.Errorf("GetMessage after delete: expected error, got nil")
	}
}

// RunMessageLargePayload verifies that a 200KB message payload survives a
// full round-trip (create + get). Exercises the path where backends must
// avoid batching the entire payload into a single oversized write.
func RunMessageLargePayload(t *testing.T, fixture BackendFixture) {
	tenant := fixture.NewTenant(t)
	c := client.NewClient(fixture.BaseURL(), tenant.Token)

	// 200KB payload — large enough to exercise large-value paths in the
	// storage layer, still within the 10MB HTTP request cap.
	largeData := strings.Repeat("L", 200*1024)
	payload := fmt.Sprintf(`"%s"`, largeData) // JSON string

	msgID, err := c.CreateMessage(t, "loan-tape-upload", payload)
	if err != nil {
		t.Fatalf("CreateMessage large payload: %v", err)
	}

	got, err := c.GetMessage(t, msgID)
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}

	content, _ := got["content"].(string)
	if len(content) < 200*1024 {
		t.Errorf("content length %d, expected >= %d", len(content), 200*1024)
	}
}
