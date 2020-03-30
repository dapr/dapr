package credentials

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func GetServerOptions(certChain *CertChain) ([]grpc.ServerOption, error) {
	opts := []grpc.ServerOption{}
	if certChain == nil {
		return opts, nil
	}

	cp := x509.NewCertPool()
	cp.AppendCertsFromPEM(certChain.RootCA)

	if certChain != nil {
		cert, err := tls.X509KeyPair(certChain.Cert, certChain.Key)
		if err != nil {
			return nil, fmt.Errorf("failed to create server certificate: %s", err)
		}

		config := &tls.Config{
			ClientCAs: cp,
			// Require cert verification
			ClientAuth:   tls.RequireAndVerifyClientCert,
			Certificates: []tls.Certificate{cert},
		}
		opts = append(opts, grpc.Creds(credentials.NewTLS(config)))
	}
	return opts, nil
}
