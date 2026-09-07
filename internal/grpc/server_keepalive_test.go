package grpc

import (
	"context"
	"net"
	"testing"
	"time"

	googlegrpc "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	cyodapb "github.com/cyoda-platform/cyoda-go/api/grpc/cyoda"
)

// The keep-alive config reaches the service: with a 20ms/60ms configuration
// a silent member is evicted in well under a second (it would take 30s at
// the old hard-wired defaults).
func TestNewServer_KeepAliveConfigReachesService(t *testing.T) {
	srv := NewServer(&fixedAuthService{uc: m2mUser()}, NewMemberRegistry(), nil, nil, nil, nil, nil, nil,
		"n", false, 0, true, nil, KeepAliveConfig{Interval: 20 * time.Millisecond, Timeout: 60 * time.Millisecond})
	if srv.service.keepAliveInterval != 20*time.Millisecond || srv.service.keepAliveTimeout != 60*time.Millisecond {
		t.Fatalf("service keep-alive = %v/%v, want 20ms/60ms", srv.service.keepAliveInterval, srv.service.keepAliveTimeout)
	}
}

// A client pinging every 5s must not be GOAWAY'd (grpc-go's default policy
// would cut it off for pinging more often than every 5 minutes).
func TestNewServer_TolerantOfFivesecondClientPings(t *testing.T) {
	srv := NewServer(&fixedAuthService{uc: m2mUser()}, NewMemberRegistry(), nil, nil, nil, nil, nil, nil,
		"n", false, 0, true, nil, KeepAliveConfig{Interval: time.Second, Timeout: 5 * time.Second})
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.GracefulStop)

	conn, err := googlegrpc.NewClient(lis.Addr().String(),
		googlegrpc.WithTransportCredentials(insecure.NewCredentials()),
		googlegrpc.WithKeepaliveParams(keepalive.ClientParameters{Time: 6 * time.Second, Timeout: time.Second, PermitWithoutStream: true}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := cyodapb.NewCloudEventsServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	stream, err := client.StartStreaming(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Hold the stream open across two client ping intervals; a GOAWAY would
	// surface as a Recv error before the deadline.
	recvErr := make(chan error, 1)
	go func() { _, err := stream.Recv(); recvErr <- err }()
	select {
	case err := <-recvErr:
		t.Fatalf("stream ended early: %v (enforcement policy too strict?)", err)
	case <-time.After(11 * time.Second):
	}
}

func m2mUser() *spi.UserContext {
	return &spi.UserContext{UserID: "m", UserName: "m", Tenant: spi.Tenant{ID: "t", Name: "t"}, Roles: []string{"ROLE_M2M"}}
}
