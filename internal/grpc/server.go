package grpc

import (
	"net"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	googlegrpc "google.golang.org/grpc"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	cyodapb "github.com/cyoda-platform/cyoda-go/api/grpc/cyoda"
	"github.com/cyoda-platform/cyoda-go/internal/contract"
	"github.com/cyoda-platform/cyoda-go/internal/domain/entity"
	"github.com/cyoda-platform/cyoda-go/internal/domain/model"
	"github.com/cyoda-platform/cyoda-go/internal/domain/search"
)

// CloudEventsServiceImpl implements the Cyoda CloudEventsService gRPC service.
type CloudEventsServiceImpl struct {
	cyodapb.UnimplementedCloudEventsServiceServer
	registry          *MemberRegistry
	authSvc           contract.AuthenticationService
	txMgr             spi.TransactionManager
	entityHandler     *entity.Handler
	modelHandler      *model.Handler
	searchService     *search.SearchService
	keepAliveInterval time.Duration
	keepAliveTimeout  time.Duration
}

// Server wraps the gRPC server.
type Server struct {
	grpcServer *googlegrpc.Server
	service    *CloudEventsServiceImpl
}

// NewServer creates a new gRPC server with auth interceptors and the
// CloudEventsService registered. When otelEnabled is true, OTel tracing
// is added via a stats handler before the auth interceptors.
func NewServer(
	authSvc contract.AuthenticationService,
	registry *MemberRegistry,
	txMgr spi.TransactionManager,
	entityHandler *entity.Handler,
	modelHandler *model.Handler,
	searchService *search.SearchService,
	otelEnabled bool,
) *Server {
	var opts []googlegrpc.ServerOption
	if otelEnabled {
		opts = append(opts, googlegrpc.StatsHandler(otelgrpc.NewServerHandler()))
	}
	opts = append(opts,
		googlegrpc.UnaryInterceptor(UnaryAuthInterceptor(authSvc)),
		googlegrpc.StreamInterceptor(StreamAuthInterceptor(authSvc)),
	)
	grpcServer := googlegrpc.NewServer(opts...)
	svc := &CloudEventsServiceImpl{
		registry:      registry,
		authSvc:       authSvc,
		txMgr:         txMgr,
		entityHandler: entityHandler,
		modelHandler:  modelHandler,
		searchService: searchService,
	}
	cyodapb.RegisterCloudEventsServiceServer(grpcServer, svc)
	return &Server{grpcServer: grpcServer, service: svc}
}

// Serve starts the gRPC server on the given listener.
func (s *Server) Serve(lis net.Listener) error {
	return s.grpcServer.Serve(lis)
}

// GracefulStop gracefully stops the gRPC server.
func (s *Server) GracefulStop() {
	s.grpcServer.GracefulStop()
}

// GRPCServer returns the underlying grpc.Server for testing.
func (s *Server) GRPCServer() *googlegrpc.Server {
	return s.grpcServer
}
