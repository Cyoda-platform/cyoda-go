package auth

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// adminReq builds an httptest request with a ROLE_ADMIN UserContext attached.
// Admin handlers are gated by requireAdmin, so tests exercising positive
// paths must use this helper (or otherwise inject an admin context).
func adminReq(method, target string, body io.Reader) *http.Request {
	return withAdminCtx(httptest.NewRequest(method, target, body))
}

// withAdminCtx attaches a UserContext holding ROLE_ADMIN to the request.
// The admin handlers (M2M, key pair, trusted keys) are wrapped by the auth
// middleware in production, which guarantees a UserContext is present; the
// role-check guard then inspects that context. Tests that want to exercise
// authorised behaviour must attach an equivalent context.
func withAdminCtx(req *http.Request) *http.Request {
	return req.WithContext(spi.WithUserContext(req.Context(), &spi.UserContext{
		UserID: "test-admin",
		Roles:  []string{"ROLE_ADMIN"},
	}))
}

// withNonAdminCtx attaches a UserContext holding only ROLE_USER.
func withNonAdminCtx(req *http.Request) *http.Request {
	return req.WithContext(spi.WithUserContext(req.Context(), &spi.UserContext{
		UserID: "test-user",
		Roles:  []string{"ROLE_USER"},
	}))
}

// assertForbidden runs the handler and expects 403 Forbidden.
func assertForbidden(t *testing.T, h http.Handler, req *http.Request, desc string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("%s: expected 403, got %d (body=%q)", desc, rec.Code, rec.Body.String())
	}
}

// assertUnauthorized runs the handler and expects 401 Unauthorized.
func assertUnauthorized(t *testing.T, h http.Handler, req *http.Request, desc string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("%s: expected 401, got %d (body=%q)", desc, rec.Code, rec.Body.String())
	}
}

// --- M2M handler ---

func TestM2MHandler_NonAdminForbidden(t *testing.T) {
	handler := NewM2MHandler(NewInMemoryM2MClientStore())

	body := `{"tenantId":"t1","userId":"u1","roles":["ROLE_USER"]}`
	cases := []struct {
		name string
		req  *http.Request
	}{
		{"create", httptest.NewRequest(http.MethodPost, "/account/m2m", bytes.NewBufferString(body))},
		{"list", httptest.NewRequest(http.MethodGet, "/account/m2m", nil)},
		{"delete", httptest.NewRequest(http.MethodDelete, "/account/m2m/some-id", nil)},
		{"reset-secret", httptest.NewRequest(http.MethodPost, "/account/m2m/some-id/secret/reset", nil)},
	}
	for _, tc := range cases {
		tc.req.Header.Set("Content-Type", "application/json")
		assertForbidden(t, handler, withNonAdminCtx(tc.req), "non-admin "+tc.name)
	}
}

func TestM2MHandler_NoUserContextUnauthorized(t *testing.T) {
	handler := NewM2MHandler(NewInMemoryM2MClientStore())

	req := httptest.NewRequest(http.MethodGet, "/account/m2m", nil)
	// Intentionally no user context — mirrors what would happen if auth
	// middleware were ever bypassed or misconfigured.
	assertUnauthorized(t, handler, req, "no-ctx list")
}

// --- Keys handler ---

func TestKeysHandler_NonAdminForbidden(t *testing.T) {
	handler := NewKeysHandler(NewInMemoryKeyStore())

	cases := []struct {
		name string
		req  *http.Request
	}{
		{"issue", httptest.NewRequest(http.MethodPost, "/oauth/keys/keypair", nil)},
		{"current", httptest.NewRequest(http.MethodGet, "/oauth/keys/keypair/current", nil)},
		{"delete", httptest.NewRequest(http.MethodDelete, "/oauth/keys/keypair/some-kid", nil)},
		{"invalidate", httptest.NewRequest(http.MethodPost, "/oauth/keys/keypair/some-kid/invalidate", nil)},
		{"reactivate", httptest.NewRequest(http.MethodPost, "/oauth/keys/keypair/some-kid/reactivate", nil)},
	}
	for _, tc := range cases {
		assertForbidden(t, handler, withNonAdminCtx(tc.req), "non-admin "+tc.name)
	}
}

func TestKeysHandler_NoUserContextUnauthorized(t *testing.T) {
	handler := NewKeysHandler(NewInMemoryKeyStore())

	req := httptest.NewRequest(http.MethodPost, "/oauth/keys/keypair", nil)
	assertUnauthorized(t, handler, req, "no-ctx issue")
}

// --- Trusted keys handler ---

func TestTrustedKeysHandler_NonAdminForbidden(t *testing.T) {
	handler := NewTrustedKeysHandler(NewInMemoryTrustedKeyStore())

	cases := []struct {
		name string
		req  *http.Request
	}{
		{"list", httptest.NewRequest(http.MethodGet, "/oauth/keys/trusted", nil)},
		{"register", httptest.NewRequest(http.MethodPost, "/oauth/keys/trusted", bytes.NewBufferString(`{}`))},
		{"delete", httptest.NewRequest(http.MethodDelete, "/oauth/keys/trusted/some-kid", nil)},
		{"invalidate", httptest.NewRequest(http.MethodPost, "/oauth/keys/trusted/some-kid/invalidate", nil)},
		{"reactivate", httptest.NewRequest(http.MethodPost, "/oauth/keys/trusted/some-kid/reactivate", nil)},
	}
	for _, tc := range cases {
		tc.req.Header.Set("Content-Type", "application/json")
		assertForbidden(t, handler, withNonAdminCtx(tc.req), "non-admin "+tc.name)
	}
}

func TestTrustedKeysHandler_NoUserContextUnauthorized(t *testing.T) {
	handler := NewTrustedKeysHandler(NewInMemoryTrustedKeyStore())

	req := httptest.NewRequest(http.MethodGet, "/oauth/keys/trusted", nil)
	assertUnauthorized(t, handler, req, "no-ctx list")
}

// --- Positive case: admin context allows through ---
// These guard against a future mistake where the guard rejects admins.

func TestM2MHandler_AdminCanList(t *testing.T) {
	handler := NewM2MHandler(NewInMemoryM2MClientStore())

	req := withAdminCtx(httptest.NewRequest(http.MethodGet, "/account/m2m", nil))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("admin list: expected 200, got %d (body=%q)", rec.Code, rec.Body.String())
	}
}

func TestKeysHandler_AdminCanIssue(t *testing.T) {
	handler := NewKeysHandler(NewInMemoryKeyStore())

	req := withAdminCtx(httptest.NewRequest(http.MethodPost, "/oauth/keys/keypair", nil))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("admin issue: expected 201, got %d (body=%q)", rec.Code, rec.Body.String())
	}
}

func TestTrustedKeysHandler_AdminCanList(t *testing.T) {
	handler := NewTrustedKeysHandler(NewInMemoryTrustedKeyStore())

	req := withAdminCtx(httptest.NewRequest(http.MethodGet, "/oauth/keys/trusted", nil))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("admin list: expected 200, got %d (body=%q)", rec.Code, rec.Body.String())
	}
}
