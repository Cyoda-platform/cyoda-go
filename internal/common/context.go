package common

import "context"

// TenantID is a named type for tenant identifiers, preventing accidental use of bare strings.
type TenantID string

// SystemTenantID is the well-known tenant for system-level data.
const SystemTenantID TenantID = "SYSTEM"

// Tenant is a first-class domain entity representing a tenant.
type Tenant struct {
	ID   TenantID
	Name string
}

// UserContext carries the authenticated user's identity through the request lifecycle.
type UserContext struct {
	UserID   string
	UserName string
	Tenant   Tenant
	Roles    []string
}

type contextKey string

const userContextKey contextKey = "userContext"

func WithUserContext(ctx context.Context, uc *UserContext) context.Context {
	return context.WithValue(ctx, userContextKey, uc)
}

func GetUserContext(ctx context.Context) *UserContext {
	uc, _ := ctx.Value(userContextKey).(*UserContext)
	return uc
}

func MustGetUserContext(ctx context.Context) *UserContext {
	uc := GetUserContext(ctx)
	if uc == nil {
		panic("UserContext not found in context — auth middleware not applied")
	}
	return uc
}

// HasRole checks whether the target role is present in the roles slice.
func HasRole(roles []string, role string) bool {
	for _, r := range roles {
		if r == role {
			return true
		}
	}
	return false
}
