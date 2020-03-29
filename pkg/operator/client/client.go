package client

import (
	"crypto/tls"
	"crypto/x509"
	"errors"

	diag "github.com/dapr/dapr/pkg/diagnostics"
	pb "github.com/dapr/dapr/pkg/proto/operator"
	"github.com/dapr/dapr/pkg/sentry/certchain"
	grpc_retry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// GetOperatorClient returns a new k8s operator client and the underlying connection.
// If a cert chain is given, a TLS connection will be established.
func GetOperatorClient(address, serverName string, certChain *certchain.CertChain) (pb.OperatorClient, *grpc.ClientConn, error) {
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
		cert, err := tls.X509KeyPair(certChain.Cert, certChain.Key)
		if err != nil {
			return nil, nil, err
		}

		config := &tls.Config{
			InsecureSkipVerify: false,
			RootCAs:            cp,
			ServerName:         serverName,
			Certificates:       []tls.Certificate{cert},
		}
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(config)))
	} else {
		opts = append(opts, grpc.WithInsecure())
	}

	conn, err := grpc.Dial(address, opts...)
	if err != nil {
		return nil, nil, err
	}
	return pb.NewOperatorClient(conn), conn, nil
}
