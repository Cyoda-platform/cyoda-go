package grpc

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	googlegrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cepb "github.com/cyoda-platform/cyoda-go/api/grpc/cloudevents"
)

// wedgingStream parks every send after the greet until released, and keeps
// the client "alive" by feeding inbound keep-alives on demand.
type wedgingStream struct {
	*mockBidiStream
	greeted chan struct{}
	release chan struct{}
	sends   int
}

func newWedgingStream(ctx context.Context) *wedgingStream {
	return &wedgingStream{mockBidiStream: newMockBidiStream(ctx), greeted: make(chan struct{}), release: make(chan struct{})}
}

func (s *wedgingStream) Send(ce *cepb.CloudEvent) error {
	s.sends++
	if s.sends == 1 {
		err := s.mockBidiStream.Send(ce)
		close(s.greeted)
		return err
	}
	<-s.release
	return s.mockBidiStream.Send(ce)
}

type bidiStream = googlegrpc.BidiStreamingServer[cepb.CloudEvent, cepb.CloudEvent]

func startStream(t *testing.T, svc *CloudEventsServiceImpl, stream bidiStream) (chan error, *Member) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- svc.StartStreaming(stream) }()
	var member *Member
	for i := 0; i < 400 && member == nil; i++ {
		if ms := svc.registry.List(); len(ms) == 1 {
			member = ms[0]
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if member == nil {
		t.Fatal("member never registered")
	}
	return done, member
}

// A member whose own keep-alive goroutine keeps pinging but whose application
// has stopped reading is evicted by write progress, within the keep-alive
// timeout, and the stream ends with "member not draining".
func TestStreaming_PingingButNotReading_IsEvictedByWriteProgress(t *testing.T) {
	svc := newServiceWithKeepAlive(20*time.Millisecond, 120*time.Millisecond)
	ctx, cancel := context.WithCancel(m2mContext("tenant-1"))
	defer cancel()
	stream := newWedgingStream(ctx)
	stream.enqueue(makeJoinEvent(t, "tenant-1", []string{"go"}))
	done, member := startStream(t, svc, stream)
	<-stream.greeted

	// Wedge the writer with a dispatch, then keep the inbound side chatty.
	go func() { _ = member.Send(context.Background(), mustCE(t)) }()
	stop := make(chan struct{})
	go func() {
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				stream.tryEnqueue(makeKeepAliveEventNoT())
			}
		}
	}()
	defer close(stop)

	select {
	case err := <-done:
		st, _ := status.FromError(err)
		if st.Code() != codes.DeadlineExceeded || !strings.Contains(st.Message(), "not draining") {
			t.Fatalf("stream ended with %v, want DeadlineExceeded 'member not draining'", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pinging-but-not-reading member was never evicted")
	}
	close(stream.release)
}

// A clean client disconnect ends the stream promptly with the Recv error and
// unregisters the member (regression guard for the single receive goroutine).
func TestStreaming_ClientClose_EndsPromptly(t *testing.T) {
	svc := newServiceWithKeepAlive(time.Hour, 2*time.Hour)
	ctx, cancel := context.WithCancel(m2mContext("tenant-1"))
	defer cancel()
	stream := newMockBidiStream(ctx)
	stream.enqueue(makeJoinEvent(t, "tenant-1", nil))
	done, member := startStream(t, svc, stream)
	_ = stream.waitForSent(t, 2*time.Second)

	stream.closeRecv()
	select {
	case err := <-done:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("err = %v, want io.EOF", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stream did not end on client close")
	}
	if svc.registry.Get(member.ID) != nil {
		t.Fatal("member still registered after close")
	}
	select {
	case <-member.WriterDone():
	case <-time.After(time.Second):
		t.Fatal("writer still running after stream end")
	}
}

// A panic inside stream.Recv is contained: ticket status, member evicted, the
// health flag untouched (this goroutine does no engine work).
func TestStreaming_RecvPanic_IsContainedWithoutLatch(t *testing.T) {
	svc := newServiceWithKeepAlive(time.Hour, 2*time.Hour)
	flag := &atomic.Bool{}
	flag.Store(true)
	svc.healthFlag = flag
	ctx, cancel := context.WithCancel(m2mContext("tenant-1"))
	defer cancel()
	stream := &panickingRecvStream{mockBidiStream: newMockBidiStream(ctx)}
	stream.enqueue(makeJoinEvent(t, "tenant-1", nil))
	done, _ := startStream(t, svc, stream)
	_ = stream.waitForSent(t, 2*time.Second)
	stream.armPanic()

	select {
	case err := <-done:
		st, _ := status.FromError(err)
		if st.Code() != codes.Internal || !strings.Contains(st.Message(), "[ticket: ") {
			t.Fatalf("err = %v, want Internal with ticket", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not end after Recv panic")
	}
	if !flag.Load() {
		t.Fatal("a receive-goroutine panic must not latch the health flag")
	}
}

// panickingRecvStream returns queued events normally until armed, then panics
// on the next Recv.
type panickingRecvStream struct {
	*mockBidiStream
	armed atomic.Bool
}

func (s *panickingRecvStream) armPanic() { s.armed.Store(true); s.enqueue(makeKeepAliveEventNoT()) }
func (s *panickingRecvStream) Recv() (*cepb.CloudEvent, error) {
	ce, err := s.mockBidiStream.Recv()
	if s.armed.Load() {
		panic("recv exploded")
	}
	return ce, err
}

// Processor, criteria and function responses count as liveness.
func TestStreaming_ResponseRefreshesLastSeen(t *testing.T) {
	svc := newServiceWithKeepAlive(time.Hour, 2*time.Hour)
	ctx, cancel := context.WithCancel(m2mContext("tenant-1"))
	defer cancel()
	stream := newMockBidiStream(ctx)
	stream.enqueue(makeJoinEvent(t, "tenant-1", nil))
	done, member := startStream(t, svc, stream)
	_ = stream.waitForSent(t, 2*time.Second)
	before := member.LastSeen()
	time.Sleep(5 * time.Millisecond)

	ce, _ := NewCloudEvent(EntityProcessorCalculationResponse, map[string]any{"requestId": "unknown", "success": true})
	stream.enqueue(ce)
	deadline := time.Now().Add(2 * time.Second)
	for !member.LastSeen().After(before) {
		if time.Now().After(deadline) {
			t.Fatal("processor response did not refresh LastSeen")
		}
		time.Sleep(2 * time.Millisecond)
	}
	stream.closeRecv()
	<-done
}
