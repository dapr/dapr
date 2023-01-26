package client

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"time"

	grpcMiddleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpcRetry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	daprCredentials "github.com/dapr/dapr/pkg/credentials"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
)

const (
	dialTimeout = 30 * time.Second
)

// GetOperatorClient returns a new k8s operator client and the underlying connection.
// If a cert chain is given, a TLS connection will be established.
func GetOperatorClient(ctx context.Context, address, serverName string, certChain *daprCredentials.CertChain) (operatorv1pb.OperatorClient, *grpc.ClientConn, error) {
	if certChain == nil {
		return nil, nil, errors.New("certificate chain cannot be nil")
	}

	unaryClientInterceptor := grpcRetry.UnaryClientInterceptor()

	if diag.DefaultGRPCMonitoring.IsEnabled() {
		unaryClientInterceptor = grpcMiddleware.ChainUnaryClient(
			unaryClientInterceptor,
			diag.DefaultGRPCMonitoring.UnaryClientInterceptor(),
		)
	}

	opts := []grpc.DialOption{grpc.WithUnaryInterceptor(unaryClientInterceptor)}

	cp := x509.NewCertPool()
	ok := cp.AppendCertsFromPEM(certChain.RootCA)
	if !ok {
		return nil, nil, errors.New("failed to append PEM root cert to x509 CertPool")
	}

	config, err := daprCredentials.TLSConfigFromCertAndKey(certChain.Cert, certChain.Key, serverName, cp)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create tls config from cert and key: %w", err)
	}
	// block for connection
	opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(config)), grpc.WithBlock())

	ctx, cancelFunc := context.WithTimeout(ctx, dialTimeout)
	defer cancelFunc()
	conn, err := grpc.DialContext(ctx, address, opts...)
	if err != nil {
		return nil, nil, err
	}
	return operatorv1pb.NewOperatorClient(conn), conn, nil
}
