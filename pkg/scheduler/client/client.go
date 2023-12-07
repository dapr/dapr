package client

import (
	"context"
	"time"

	grpcMiddleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpcRetry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"google.golang.org/grpc"

	diag "github.com/dapr/dapr/pkg/diagnostics"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/security"
)

const (
	dialTimeout = 30 * time.Second
)

// GetSchedulerClient returns a new scheduler client and the underlying connection.
// If a cert chain is given, a TLS connection will be established.
func GetSchedulerClient(ctx context.Context, address string, sec security.Handler) (schedulerv1pb.SchedulerClient, *grpc.ClientConn, error) {
	unaryClientInterceptor := grpcRetry.UnaryClientInterceptor()

	if diag.DefaultGRPCMonitoring.IsEnabled() {
		unaryClientInterceptor = grpcMiddleware.ChainUnaryClient(
			unaryClientInterceptor,
			diag.DefaultGRPCMonitoring.UnaryClientInterceptor(),
		)
	}

	schedulerID, err := spiffeid.FromSegments(sec.ControlPlaneTrustDomain(), "ns", sec.ControlPlaneNamespace(), "dapr-scheduler")
	if err != nil {
		return nil, nil, err
	}

	opts := []grpc.DialOption{
		grpc.WithUnaryInterceptor(unaryClientInterceptor),
		sec.GRPCDialOptionMTLS(schedulerID), grpc.WithReturnConnectionError(),
	}

	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	conn, err := grpc.DialContext(ctx, address, opts...)
	if err != nil {
		return nil, nil, err
	}
	return schedulerv1pb.NewSchedulerClient(conn), conn, nil
}
