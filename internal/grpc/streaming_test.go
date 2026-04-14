package grpc

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"

	cepb "github.com/cyoda-platform/cyoda-go/api/grpc/cloudevents"
	"github.com/cyoda-platform/cyoda-go/internal/common"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// mockBidiStream simulates a BidiStreamingServer for testing StartStreaming.
type mockBidiStream struct {
	ctx     context.Context
	recvCh  chan *cepb.CloudEvent
	sentMu  sync.Mutex
	sent    []*cepb.CloudEvent
	sentCh  chan *cepb.CloudEvent // optional: signals when a message is sent
	recvErr error                // if set, Recv returns this after recvCh is drained
}

func newMockBidiStream(ctx context.Context) *mockBidiStream {
	return &mockBidiStream{
		ctx:    ctx,
		recvCh: make(chan *cepb.CloudEvent, 32),
		sentCh: make(chan *cepb.CloudEvent, 32),
	}
}

func (m *mockBidiStream) Send(ce *cepb.CloudEvent) error {
	m.sentMu.Lock()
	m.sent = append(m.sent, ce)
	m.sentMu.Unlock()
	// Non-blocking signal.
	select {
	case m.sentCh <- ce:
	default:
	}
	return nil
}

func (m *mockBidiStream) Recv() (*cepb.CloudEvent, error) {
	select {
	case ce, ok := <-m.recvCh:
		if !ok {
			if m.recvErr != nil {
				return nil, m.recvErr
			}
			return nil, io.EOF
		}
		return ce, nil
	case <-m.ctx.Done():
		return nil, m.ctx.Err()
	}
}

func (m *mockBidiStream) Context() context.Context     { return m.ctx }
func (m *mockBidiStream) SetHeader(_ metadata.MD) error { return nil }
func (m *mockBidiStream) SendHeader(_ metadata.MD) error { return nil }
func (m *mockBidiStream) SetTrailer(_ metadata.MD)      {}
func (m *mockBidiStream) SendMsg(_ any) error           { return nil }
func (m *mockBidiStream) RecvMsg(_ any) error           { return nil }

// sentMessages returns a snapshot of all sent messages.
func (m *mockBidiStream) sentMessages() []*cepb.CloudEvent {
	m.sentMu.Lock()
	defer m.sentMu.Unlock()
	cp := make([]*cepb.CloudEvent, len(m.sent))
	copy(cp, m.sent)
	return cp
}

// waitForSent blocks until a message is sent or times out.
func (m *mockBidiStream) waitForSent(t *testing.T, timeout time.Duration) *cepb.CloudEvent {
	t.Helper()
	select {
	case ce := <-m.sentCh:
		return ce
	case <-time.After(timeout):
		t.Fatal("timed out waiting for sent message")
		return nil
	}
}

// enqueue adds a message to the receive channel (simulating client sending).
func (m *mockBidiStream) enqueue(ce *cepb.CloudEvent) {
	m.recvCh <- ce
}

// closeRecv closes the receive channel, causing Recv to return io.EOF.
func (m *mockBidiStream) closeRecv() {
	close(m.recvCh)
}

// --- helpers ---

func makeJoinEvent(t *testing.T, tenantID string, tags []string) *cepb.CloudEvent {
	t.Helper()
	payload := map[string]any{
		"id":                  "join-event-1",
		"tags":                tags,
		"joinedLegalEntityId": tenantID,
	}
	ce, err := NewCloudEvent(CalculationMemberJoinEvent, payload)
	if err != nil {
		t.Fatalf("failed to create join event: %v", err)
	}
	return ce
}

func makeKeepAliveEvent(t *testing.T) *cepb.CloudEvent {
	t.Helper()
	ce, err := NewCloudEvent(CalculationMemberKeepAliveEvent, map[string]any{"success": true})
	if err != nil {
		t.Fatalf("failed to create keep alive event: %v", err)
	}
	return ce
}

func newServiceForTest() *CloudEventsServiceImpl {
	return &CloudEventsServiceImpl{
		registry: NewMemberRegistry(),
	}
}

func m2mContext(tenantID common.TenantID) context.Context {
	return common.WithUserContext(context.Background(), &common.UserContext{
		UserID:   "m2m-client",
		UserName: "m2m",
		Tenant:   common.Tenant{ID: tenantID, Name: "Test Tenant"},
		Roles:    []string{"ROLE_M2M"},
	})
}

func nonM2MContext(tenantID common.TenantID) context.Context {
	return common.WithUserContext(context.Background(), &common.UserContext{
		UserID:   "user-1",
		UserName: "alice",
		Tenant:   common.Tenant{ID: tenantID, Name: "Test Tenant"},
		Roles:    []string{"ROLE_USER"},
	})
}

// --- tests ---

func TestStreaming_JoinAndGreet(t *testing.T) {
	svc := newServiceForTest()
	ctx, cancel := context.WithCancel(m2mContext("tenant-1"))
	defer cancel()

	stream := newMockBidiStream(ctx)

	// Send join event.
	stream.enqueue(makeJoinEvent(t, "tenant-1", []string{"python", "go"}))

	// Close recv after join so the loop exits with EOF.
	// But we need to wait for the greet to be sent first, so close after a delay.
	done := make(chan error, 1)
	go func() {
		done <- svc.StartStreaming(stream)
	}()

	// Wait for greet event.
	greetCE := stream.waitForSent(t, 2*time.Second)
	if greetCE.Type != CalculationMemberGreetEvent {
		t.Fatalf("expected greet event type %s, got %s", CalculationMemberGreetEvent, greetCE.Type)
	}

	// Parse greet payload.
	_, greetPayload, err := ParseCloudEvent(greetCE)
	if err != nil {
		t.Fatalf("failed to parse greet event: %v", err)
	}
	var greet struct {
		MemberID            string `json:"memberId"`
		JoinedLegalEntityID string `json:"joinedLegalEntityId"`
		Success             bool   `json:"success"`
	}
	if err := json.Unmarshal(greetPayload, &greet); err != nil {
		t.Fatalf("failed to unmarshal greet payload: %v", err)
	}
	if greet.MemberID == "" {
		t.Fatal("expected non-empty memberId in greet event")
	}
	if greet.JoinedLegalEntityID != "tenant-1" {
		t.Errorf("expected joinedLegalEntityId 'tenant-1', got %q", greet.JoinedLegalEntityID)
	}
	if !greet.Success {
		t.Error("expected success=true in greet event")
	}

	// Close the stream.
	stream.closeRecv()

	err = <-done
	if err != nil && err != io.EOF {
		t.Fatalf("unexpected error from StartStreaming: %v", err)
	}
}

func TestStreaming_MemberRegisteredAndUnregistered(t *testing.T) {
	svc := newServiceForTest()
	ctx, cancel := context.WithCancel(m2mContext("tenant-1"))
	defer cancel()

	stream := newMockBidiStream(ctx)
	stream.enqueue(makeJoinEvent(t, "tenant-1", []string{"python"}))

	done := make(chan error, 1)
	go func() {
		done <- svc.StartStreaming(stream)
	}()

	// Wait for greet.
	greetCE := stream.waitForSent(t, 2*time.Second)
	_, greetPayload, _ := ParseCloudEvent(greetCE)
	memberID := ExtractStringField(greetPayload, "memberId")

	// Verify member is registered.
	member := svc.registry.Get(memberID)
	if member == nil {
		t.Fatal("expected member to be registered after greet")
	}
	if member.TenantID != "tenant-1" {
		t.Errorf("expected tenant 'tenant-1', got %q", member.TenantID)
	}

	// Close stream to trigger unregister.
	stream.closeRecv()
	<-done

	// Verify member is unregistered.
	member = svc.registry.Get(memberID)
	if member != nil {
		t.Fatal("expected member to be unregistered after stream close")
	}
}

func TestStreaming_KeepAliveExchange(t *testing.T) {
	svc := newServiceForTest()
	ctx, cancel := context.WithCancel(m2mContext("tenant-1"))
	defer cancel()

	stream := newMockBidiStream(ctx)
	stream.enqueue(makeJoinEvent(t, "tenant-1", []string{}))

	done := make(chan error, 1)
	go func() {
		done <- svc.StartStreaming(stream)
	}()

	// Wait for greet.
	_ = stream.waitForSent(t, 2*time.Second)

	// Send keep-alive.
	stream.enqueue(makeKeepAliveEvent(t))

	// Wait for keep-alive response.
	kaCE := stream.waitForSent(t, 2*time.Second)
	if kaCE.Type != CalculationMemberKeepAliveEvent {
		t.Fatalf("expected keep-alive response type %s, got %s", CalculationMemberKeepAliveEvent, kaCE.Type)
	}

	_, kaPayload, _ := ParseCloudEvent(kaCE)
	var kaResp struct {
		MemberID string `json:"memberId"`
		Success  bool   `json:"success"`
	}
	if err := json.Unmarshal(kaPayload, &kaResp); err != nil {
		t.Fatalf("failed to unmarshal keep-alive response: %v", err)
	}
	if !kaResp.Success {
		t.Error("expected success=true in keep-alive response")
	}
	if kaResp.MemberID == "" {
		t.Error("expected non-empty memberId in keep-alive response")
	}

	// Close.
	stream.closeRecv()
	<-done
}

func TestStreaming_NoRoleM2M_PermissionDenied(t *testing.T) {
	svc := newServiceForTest()
	ctx := nonM2MContext("tenant-1")

	stream := newMockBidiStream(ctx)

	err := svc.StartStreaming(stream)
	if err == nil {
		t.Fatal("expected error for non-M2M user")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", st.Code())
	}
}

func TestStreaming_NoUserContext_Unauthenticated(t *testing.T) {
	svc := newServiceForTest()
	ctx := context.Background() // no UserContext

	stream := newMockBidiStream(ctx)

	err := svc.StartStreaming(stream)
	if err == nil {
		t.Fatal("expected error for missing user context")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.Unauthenticated {
		t.Errorf("expected Unauthenticated, got %v", st.Code())
	}
}

func TestStreaming_FirstMessageNotJoin_InvalidArgument(t *testing.T) {
	svc := newServiceForTest()
	ctx := m2mContext("tenant-1")

	stream := newMockBidiStream(ctx)

	// Send a keep-alive as the first message instead of a join.
	stream.enqueue(makeKeepAliveEvent(t))

	err := svc.StartStreaming(stream)
	if err == nil {
		t.Fatal("expected error for non-join first message")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}
}

func TestStreaming_TenantMismatch_PermissionDenied(t *testing.T) {
	svc := newServiceForTest()
	ctx := m2mContext("tenant-1")

	stream := newMockBidiStream(ctx)

	// Join with a different tenant.
	stream.enqueue(makeJoinEvent(t, "tenant-2", nil))

	err := svc.StartStreaming(stream)
	if err == nil {
		t.Fatal("expected error for tenant mismatch")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", st.Code())
	}
}

func TestStreaming_ProcessorResponse(t *testing.T) {
	svc := newServiceForTest()
	ctx, cancel := context.WithCancel(m2mContext("tenant-1"))
	defer cancel()

	stream := newMockBidiStream(ctx)
	stream.enqueue(makeJoinEvent(t, "tenant-1", []string{"python"}))

	done := make(chan error, 1)
	go func() {
		done <- svc.StartStreaming(stream)
	}()

	// Wait for greet to get memberID.
	greetCE := stream.waitForSent(t, 2*time.Second)
	_, greetPayload, _ := ParseCloudEvent(greetCE)
	memberID := ExtractStringField(greetPayload, "memberId")

	// Track a request on the member.
	member := svc.registry.Get(memberID)
	if member == nil {
		t.Fatal("member not found")
	}
	respCh := member.TrackRequest("req-123")

	// Send processor response from the "client".
	respPayload := map[string]any{
		"requestId": "req-123",
		"success":   true,
		"payload":   map[string]any{"data": map[string]any{"updated": true}},
	}
	respCE, err := NewCloudEvent(EntityProcessorCalculationResponse, respPayload)
	if err != nil {
		t.Fatalf("failed to create response event: %v", err)
	}
	stream.enqueue(respCE)

	// Wait for the response to be routed.
	select {
	case resp := <-respCh:
		if resp == nil {
			t.Fatal("expected non-nil response")
		}
		if !resp.Success {
			t.Error("expected success=true")
		}
		if resp.Payload == nil {
			t.Error("expected non-nil payload")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for processor response")
	}

	// Close.
	stream.closeRecv()
	<-done
}

func TestStreaming_CriteriaResponse(t *testing.T) {
	svc := newServiceForTest()
	ctx, cancel := context.WithCancel(m2mContext("tenant-1"))
	defer cancel()

	stream := newMockBidiStream(ctx)
	stream.enqueue(makeJoinEvent(t, "tenant-1", []string{"python"}))

	done := make(chan error, 1)
	go func() {
		done <- svc.StartStreaming(stream)
	}()

	// Wait for greet.
	greetCE := stream.waitForSent(t, 2*time.Second)
	_, greetPayload, _ := ParseCloudEvent(greetCE)
	memberID := ExtractStringField(greetPayload, "memberId")

	member := svc.registry.Get(memberID)
	if member == nil {
		t.Fatal("member not found")
	}
	respCh := member.TrackRequest("req-456")

	// Send criteria response.
	respPayload := map[string]any{
		"requestId": "req-456",
		"success":   true,
		"matches":   true,
	}
	respCE, err := NewCloudEvent(EntityCriteriaCalculationResponse, respPayload)
	if err != nil {
		t.Fatalf("failed to create criteria response event: %v", err)
	}
	stream.enqueue(respCE)

	select {
	case resp := <-respCh:
		if resp == nil {
			t.Fatal("expected non-nil response")
		}
		if !resp.Success {
			t.Error("expected success=true")
		}
		if resp.Matches == nil || !*resp.Matches {
			t.Error("expected matches=true")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for criteria response")
	}

	stream.closeRecv()
	<-done
}

func TestStreaming_KeepAliveTimeout(t *testing.T) {
	svc := newServiceForTest()
	// Set very short keep-alive for testing.
	svc.SetKeepAliveConfig(50*time.Millisecond, 100*time.Millisecond)

	ctx, cancel := context.WithCancel(m2mContext("tenant-1"))
	defer cancel()

	stream := newMockBidiStream(ctx)
	stream.enqueue(makeJoinEvent(t, "tenant-1", []string{}))

	done := make(chan error, 1)
	go func() {
		done <- svc.StartStreaming(stream)
	}()

	// Wait for greet.
	_ = stream.waitForSent(t, 2*time.Second)

	// Do NOT send any keep-alive — let the timeout fire.
	// The keep-alive loop should cancel the context after ~100ms.
	select {
	case err := <-done:
		// Stream should end due to context cancellation.
		if err == nil {
			// Also acceptable — the stream just ended.
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for keep-alive timeout to terminate stream")
	}
}

func TestStreaming_EmptyTenantInJoinEvent_UsesAuthTenant(t *testing.T) {
	svc := newServiceForTest()
	ctx, cancel := context.WithCancel(m2mContext("tenant-1"))
	defer cancel()

	stream := newMockBidiStream(ctx)

	// Join with empty joinedLegalEntityId — should use the auth tenant.
	joinPayload := map[string]any{
		"id":                  "join-event-2",
		"tags":                []string{"python"},
		"joinedLegalEntityId": "",
	}
	joinCE, err := NewCloudEvent(CalculationMemberJoinEvent, joinPayload)
	if err != nil {
		t.Fatalf("failed to create join event: %v", err)
	}
	stream.enqueue(joinCE)

	done := make(chan error, 1)
	go func() {
		done <- svc.StartStreaming(stream)
	}()

	// Wait for greet — should succeed.
	greetCE := stream.waitForSent(t, 2*time.Second)
	if greetCE.Type != CalculationMemberGreetEvent {
		t.Fatalf("expected greet event, got %s", greetCE.Type)
	}

	_, greetPayload, _ := ParseCloudEvent(greetCE)
	memberID := ExtractStringField(greetPayload, "memberId")

	// Verify member registered with auth tenant.
	member := svc.registry.Get(memberID)
	if member == nil {
		t.Fatal("member not registered")
	}
	if member.TenantID != "tenant-1" {
		t.Errorf("expected tenant 'tenant-1', got %q", member.TenantID)
	}

	stream.closeRecv()
	<-done
}

func TestHasRole(t *testing.T) {
	tests := []struct {
		name   string
		roles  []string
		target string
		want   bool
	}{
		{"present", []string{"ROLE_USER", "ROLE_M2M"}, "ROLE_M2M", true},
		{"absent", []string{"ROLE_USER"}, "ROLE_M2M", false},
		{"empty", nil, "ROLE_M2M", false},
		{"exact match only", []string{"ROLE_M2M_ADMIN"}, "ROLE_M2M", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := common.HasRole(tt.roles, tt.target); got != tt.want {
				t.Errorf("HasRole(%v, %q) = %v, want %v", tt.roles, tt.target, got, tt.want)
			}
		})
	}
}
