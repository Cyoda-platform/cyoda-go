package grpc

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/cyoda-platform/cyoda-go/internal/common"
	googlegrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// mockAuthService is a test double for spi.AuthenticationService.
type mockAuthService struct {
	user *common.UserContext
	err  error
}

func (m *mockAuthService) Authenticate(_ context.Context, _ *http.Request) (*common.UserContext, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.user, nil
}

// mockServerStream is a minimal test double for grpc.ServerStream.
type mockServerStream struct {
	googlegrpc.ServerStream
	ctx context.Context
}

func (m *mockServerStream) Context() context.Context { return m.ctx }

func TestInterceptor_UnarySuccess(t *testing.T) {
	uc := &common.UserContext{
		UserID:   "user-1",
		UserName: "alice",
		Tenant:   common.Tenant{ID: "tenant-1", Name: "Tenant One"},
		Roles:    []string{"admin"},
	}
	authSvc := &mockAuthService{user: uc}
	interceptor := UnaryAuthInterceptor(authSvc)

	md := metadata.Pairs("authorization", "Bearer test-token")
	ctx := metadata.NewIncomingContext(context.Background(), md)

	var handlerCtx context.Context
	handler := func(ctx context.Context, req any) (any, error) {
		handlerCtx = ctx
		return "ok", nil
	}

	resp, err := interceptor(ctx, "request", &googlegrpc.UnaryServerInfo{}, handler)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp != "ok" {
		t.Fatalf("expected resp 'ok', got %v", resp)
	}

	got := common.GetUserContext(handlerCtx)
	if got == nil {
		t.Fatal("expected UserContext in handler context, got nil")
	}
	if got.UserID != "user-1" {
		t.Errorf("expected UserID 'user-1', got %q", got.UserID)
	}
	if got.UserName != "alice" {
		t.Errorf("expected UserName 'alice', got %q", got.UserName)
	}
	if got.Tenant.ID != "tenant-1" {
		t.Errorf("expected TenantID 'tenant-1', got %q", got.Tenant.ID)
	}
}

func TestInterceptor_UnaryAuthFailure(t *testing.T) {
	authSvc := &mockAuthService{err: errors.New("invalid token")}
	interceptor := UnaryAuthInterceptor(authSvc)

	ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{})

	handler := func(_ context.Context, _ any) (any, error) {
		t.Fatal("handler should not be called on auth failure")
		return nil, nil
	}

	_, err := interceptor(ctx, "request", &googlegrpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}, handler)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.Unauthenticated {
		t.Errorf("expected code Unauthenticated, got %v", st.Code())
	}
	if st.Message() != "authentication failed" {
		t.Errorf("expected generic message 'authentication failed', got %q", st.Message())
	}
	if strings.Contains(st.Message(), "invalid token") {
		t.Error("error message must not contain internal auth error details")
	}
}

func TestInterceptor_StreamSuccess(t *testing.T) {
	uc := &common.UserContext{
		UserID:   "user-2",
		UserName: "bob",
		Tenant:   common.Tenant{ID: "tenant-2", Name: "Tenant Two"},
		Roles:    []string{"reader"},
	}
	authSvc := &mockAuthService{user: uc}
	interceptor := StreamAuthInterceptor(authSvc)

	md := metadata.Pairs("authorization", "Bearer stream-token")
	ctx := metadata.NewIncomingContext(context.Background(), md)
	stream := &mockServerStream{ctx: ctx}

	var handlerStream googlegrpc.ServerStream
	handler := func(_ any, ss googlegrpc.ServerStream) error {
		handlerStream = ss
		return nil
	}

	err := interceptor(nil, stream, &googlegrpc.StreamServerInfo{}, handler)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	got := common.GetUserContext(handlerStream.Context())
	if got == nil {
		t.Fatal("expected UserContext in stream context, got nil")
	}
	if got.UserID != "user-2" {
		t.Errorf("expected UserID 'user-2', got %q", got.UserID)
	}
	if got.Tenant.ID != "tenant-2" {
		t.Errorf("expected TenantID 'tenant-2', got %q", got.Tenant.ID)
	}
}

func TestInterceptor_StreamAuthFailure(t *testing.T) {
	authSvc := &mockAuthService{err: errors.New("expired token")}
	interceptor := StreamAuthInterceptor(authSvc)

	ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{})
	stream := &mockServerStream{ctx: ctx}

	handler := func(_ any, _ googlegrpc.ServerStream) error {
		t.Fatal("handler should not be called on auth failure")
		return nil
	}

	err := interceptor(nil, stream, &googlegrpc.StreamServerInfo{FullMethod: "/test.Service/Stream"}, handler)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.Unauthenticated {
		t.Errorf("expected code Unauthenticated, got %v", st.Code())
	}
	if st.Message() != "authentication failed" {
		t.Errorf("expected generic message 'authentication failed', got %q", st.Message())
	}
	if strings.Contains(st.Message(), "expired token") {
		t.Error("error message must not contain internal auth error details")
	}
}
