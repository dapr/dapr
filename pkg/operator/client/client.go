package client

import (
	"crypto/x509"
	"errors"
	"fmt"

	dapr_credentials "github.com/dapr/dapr/pkg/credentials"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	grpc_retry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// GetOperatorClient returns a new k8s operator client and the underlying connection.
// If a cert chain is given, a TLS connection will be established.
func GetOperatorClient(address, serverName string, certChain *dapr_credentials.CertChain) (operatorv1pb.OperatorClient, *grpc.ClientConn, error) {
	opts := []grpc.DialOption{
		grpc.WithStatsHandler(diag.DefaultGRPCMonitoring.ClientStatsHandler),
		grpc.WithUnaryInterceptor(grpc_retry.UnaryClientInterceptor()),
	}
	if certChain != nil {
		cp := x509.NewCertPool()
		ok := cp.AppendCertsFromPEM(certChain.RootCA)
		if !ok {
			return nil, nil, errors.New("failed to append PEM root cert to x509 CertPool")
		}

		config, err := dapr_credentials.TLSConfigFromCertAndKey(certChain.Cert, certChain.Key, serverName, cp)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create tls config from cert and key: %s", err)
		}
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(config)))
	} else {
		opts = append(opts, grpc.WithInsecure())
	}

	conn, err := grpc.Dial(address, opts...)
	if err != nil {
		return nil, nil, err
	}
	return operatorv1pb.NewOperatorClient(conn), conn, nil
}
