package grpc

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	googlegrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	cyodapb "github.com/cyoda-platform/cyoda-go/api/grpc/cyoda"
)

// A member that joins, then stops reading while the server keeps writing,
// fills the HTTP/2 write window (pinned to 64 KiB on the client) and is
// evicted by the write-progress rule within the keep-alive timeout; every
// dispatcher queued behind that stalled write is released, and the writer
// itself unwinds once the handler returns.
func TestFrozenMember_IsEvictedAndDispatchersAreReleased(t *testing.T) {
	ka := KeepAliveConfig{Interval: 100 * time.Millisecond, Timeout: 500 * time.Millisecond}
	ts := startRecoveryTestServerWithKeepAlive(t, nil, ka)

	conn, err := googlegrpc.NewClient(ts.addr,
		googlegrpc.WithTransportCredentials(insecure.NewCredentials()),
		googlegrpc.WithInitialWindowSize(65535),
		googlegrpc.WithInitialConnWindowSize(65535))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := cyodapb.NewCloudEventsServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	stream, err := client.StartStreaming(ctx)
	if err != nil {
		t.Fatal(err)
	}
	join, err := NewCloudEvent(CalculationMemberJoinEvent, map[string]any{
		"id":                  "j",
		"tags":                []string{"frozen"},
		"joinedLegalEntityId": string(ts.uc.Tenant.ID),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(join); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil { // the greet
		t.Fatalf("greet: %v", err)
	}
	// From here the client never calls Recv again: frozen. Its keep-alive
	// goroutine keeps pinging, though — that is what a frozen compute node
	// looks like, and it takes the inbound-silence rule out of play, so the
	// only rule left that can evict this member is write progress.
	feederDone := make(chan struct{})
	go func() {
		defer close(feederDone)
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if err := stream.Send(makeKeepAliveEvent()); err != nil {
					return
				}
			}
		}
	}()

	t.Cleanup(func() { cancel(); <-feederDone })

	var member *Member
	for i := 0; i < 200 && member == nil; i++ {
		if ms := ts.registry.List(); len(ms) == 1 {
			member = ms[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	if member == nil {
		t.Fatal("member not registered")
	}

	// Big events, handed to the writer one at a time. How much a frozen client
	// absorbs before the writer wedges is not a fixed number — its receive
	// window, the socket buffers, and the one oversized write the server's
	// per-stream write quota lets through all contribute — so a filler keeps
	// pushing until the stall is observed rather than assuming one size fills
	// the pipe.
	big := make([]byte, 1024*1024)
	for i := range big {
		big[i] = 'x'
	}
	payload := map[string]any{"requestId": "big", "payload": string(big)}
	ce, err := NewCloudEvent(EntityProcessorCalculationRequest, payload)
	if err != nil {
		t.Fatal(err)
	}
	fillerDone := make(chan struct{})
	go func() {
		defer close(fillerDone)
		for {
			fctx, fcancel := context.WithTimeout(context.Background(), 2*time.Second)
			err := member.Send(fctx, ce)
			fcancel()
			if err != nil { // evicted, or the writer has stopped taking events
				return
			}
		}
	}()
	t.Cleanup(func() { <-fillerDone })

	// The writer must be stuck in a raw send, not merely idle: that stall is
	// what the keep-alive loop reads.
	var wedgedAt time.Time
	for i := 0; i < 500 && wedgedAt.IsZero(); i++ {
		wedgedAt = member.WriteInFlightSince()
		if wedgedAt.IsZero() {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if wedgedAt.IsZero() {
		t.Fatal("writer never wedged: the frozen client kept draining")
	}

	// Five dispatchers now queue behind that wedged write. Not one of them may
	// stay wedged with it: eviction, or its own deadline, has to release each.
	var released atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sctx, scancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer scancel()
			_ = member.Send(sctx, ce)
			released.Add(1)
		}()
	}

	select {
	case <-member.Evicted():
	case <-time.After(5 * time.Second):
		t.Fatal("frozen member was not evicted within the keep-alive timeout")
	}
	// The stall, not inbound silence, is what must have evicted it: a member
	// that never sends a keep-alive would eventually go on the LastSeen rule
	// too, and that would prove nothing about the writer.
	st, ok := status.FromError(member.EvictErr())
	if !ok || st.Code() != codes.DeadlineExceeded || st.Message() != "member not draining" {
		t.Fatalf("expected eviction by the write-progress rule, got %v", member.EvictErr())
	}
	wg.Wait()
	if released.Load() != 5 {
		t.Fatalf("%d of 5 senders released", released.Load())
	}
	select {
	case <-member.WriterDone():
	case <-time.After(5 * time.Second):
		t.Fatal("writer still wedged after eviction: handler return did not unblock the raw send")
	}
}

// A connection whose bytes stop flowing in both directions (a pausable TCP
// proxy) is torn down by the transport keepalive within Time+Timeout and the
// member is unregistered, even though nothing at the application level ever
// errors.
func TestBlackholedConnection_IsTornDownByTransportKeepalive(t *testing.T) {
	ka := KeepAliveConfig{Interval: 1 * time.Second, Timeout: 1 * time.Second}
	ts := startRecoveryTestServerWithKeepAlive(t, nil, ka)
	proxy := newPausableProxy(t, ts.addr)

	conn, err := googlegrpc.NewClient(proxy.addr, googlegrpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := cyodapb.NewCloudEventsServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stream, err := client.StartStreaming(ctx)
	if err != nil {
		t.Fatal(err)
	}
	join, err := NewCloudEvent(CalculationMemberJoinEvent, map[string]any{
		"id":                  "j",
		"tags":                []string{"bh"},
		"joinedLegalEntityId": string(ts.uc.Tenant.ID),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(join); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("greet: %v", err)
	}
	proxy.pause() // both directions stop; TCP stays open.

	deadline := time.Now().Add(10 * time.Second) // Time+Timeout is 2s; allow slack
	for len(ts.registry.List()) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("black-holed member still registered")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// The application keep-alive fires on the same clock, so the member being
	// gone does not by itself prove the transport noticed. GracefulStop waits
	// for every connection to close: with transport keepalive the dead one is
	// torn down and Stop returns; without it, Stop would wait on a connection
	// that never drains (grpc-go's default is a two-hour keepalive).
	stopped := make(chan struct{})
	go func() { ts.srv.GracefulStop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("GracefulStop hung: the black-holed connection was never closed by transport keepalive")
	}
}

// pausableProxy forwards bytes between a client and target until pause() is
// called, after which it silently drops everything while keeping both TCP
// connections open.
type pausableProxy struct {
	addr   string
	paused atomic.Bool
}

func newPausableProxy(t *testing.T, target string) *pausableProxy {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &pausableProxy{addr: lis.Addr().String()}
	t.Cleanup(func() { _ = lis.Close() })
	go func() {
		for {
			c, err := lis.Accept()
			if err != nil {
				return
			}
			up, err := net.Dial("tcp", target)
			if err != nil {
				_ = c.Close()
				continue
			}
			pump := func(dst, src net.Conn) {
				defer func() { _ = dst.Close(); _ = src.Close() }()
				buf := make([]byte, 32*1024)
				for {
					n, err := src.Read(buf)
					if n > 0 && !p.paused.Load() {
						if _, werr := dst.Write(buf[:n]); werr != nil {
							return
						}
					}
					if err != nil {
						return
					}
				}
			}
			go pump(up, c)
			go pump(c, up)
		}
	}()
	return p
}

func (p *pausableProxy) pause() { p.paused.Store(true) }
