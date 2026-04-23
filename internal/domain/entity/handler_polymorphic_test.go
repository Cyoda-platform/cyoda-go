package entity

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/cyoda-platform/cyoda-go/internal/common"
	"github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"
)

// TestClassifyValidateOrExtendErr_PolymorphicSlot asserts that a
// schema.ErrPolymorphicSlot-wrapped error is classified as an operational
// 4xx with the dedicated POLYMORPHIC_SLOT code — NOT a generic BAD_REQUEST
// and NOT a 5xx internal error. SDKs detect this code to display the
// "normalize the field" guidance instead of the misleading
// "change level violation" text previously exposed.
func TestClassifyValidateOrExtendErr_PolymorphicSlot(t *testing.T) {
	// Wrap the sentinel the same way schema.Extend does at its call site.
	underlying := fmt.Errorf("%w at %q: existing %s, incoming %s — normalize the field",
		schema.ErrPolymorphicSlot, ".roles_and_permissions.custom_permissions", "ARRAY", "LEAF")

	appErr := classifyValidateOrExtendErr(underlying)
	if appErr == nil {
		t.Fatal("classifyValidateOrExtendErr returned nil")
	}
	if appErr.Status != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", appErr.Status, http.StatusBadRequest)
	}
	if appErr.Code != common.ErrCodePolymorphicSlot {
		t.Errorf("code = %q, want %q", appErr.Code, common.ErrCodePolymorphicSlot)
	}
	if strings.Contains(appErr.Message, "change level violation") {
		t.Errorf("message must NOT say 'change level violation' for polymorphic slot (misleading); got: %q", appErr.Message)
	}
	if !strings.Contains(appErr.Message, "polymorphic") {
		t.Errorf("message must name polymorphism so clients can search docs; got: %q", appErr.Message)
	}
}

// TestClassifyValidateOrExtendErr_ChangeLevelViolation_StillGetsBadRequest
// — genuine change-level violations keep the existing classification path:
// 4xx BAD_REQUEST, with the message still describing the level mismatch.
func TestClassifyValidateOrExtendErr_ChangeLevelViolation_StillGetsBadRequest(t *testing.T) {
	underlying := fmt.Errorf("change level violation: new field %q at %s requires STRUCTURAL level, but level is %q",
		"b", ".b", "TYPE")

	appErr := classifyValidateOrExtendErr(underlying)
	if appErr == nil {
		t.Fatal("nil appErr")
	}
	if appErr.Status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", appErr.Status)
	}
	if appErr.Code != common.ErrCodeBadRequest {
		t.Errorf("code = %q, want %q (not polymorphic)", appErr.Code, common.ErrCodeBadRequest)
	}
}

// TestClassifyValidateOrExtendErr_InternalFailure_Still5xx — "failed to
// extend schema" (plugin-layer write failure) is still classified as 5xx
// with a ticket UUID, unchanged.
func TestClassifyValidateOrExtendErr_InternalFailure_Still5xx(t *testing.T) {
	underlying := fmt.Errorf("failed to extend schema: write rejected: %w", fmt.Errorf("pgx: connection refused"))

	appErr := classifyValidateOrExtendErr(underlying)
	if appErr == nil {
		t.Fatal("nil appErr")
	}
	if appErr.Status != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", appErr.Status)
	}
}
