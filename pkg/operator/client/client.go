package client

import (
	"context"
	"time"

	grpcMiddleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpcRetry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"google.golang.org/grpc"

	diag "github.com/dapr/dapr/pkg/diagnostics"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/security"
)

const (
	dialTimeout = 30 * time.Second
)

// GetOperatorClient returns a new k8s operator client and the underlying connection.
// If a cert chain is given, a TLS connection will be established.
func GetOperatorClient(ctx context.Context, address string, sec security.Handler) (operatorv1pb.OperatorClient, *grpc.ClientConn, error) {
	unaryClientInterceptor := grpcRetry.UnaryClientInterceptor()

	if diag.DefaultGRPCMonitoring.IsEnabled() {
		unaryClientInterceptor = grpcMiddleware.ChainUnaryClient(
			unaryClientInterceptor,
			diag.DefaultGRPCMonitoring.UnaryClientInterceptor(),
		)
	}

	operatorID, err := spiffeid.FromSegments(sec.ControlPlaneTrustDomain(), "ns", sec.ControlPlaneNamespace(), "dapr-operator")
	if err != nil {
		return nil, nil, err
	}

	opts := []grpc.DialOption{
		grpc.WithUnaryInterceptor(unaryClientInterceptor),
		sec.GRPCDialOptionMTLS(operatorID), grpc.WithReturnConnectionError(),
	}

	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	conn, err := grpc.DialContext(ctx, address, opts...)
	if err != nil {
		return nil, nil, err
	}
	return operatorv1pb.NewOperatorClient(conn), conn, nil
}
