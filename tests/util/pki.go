/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package util

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/bundle/x509bundle"
	"github.com/spiffe/go-spiffe/v2/spiffegrpc/grpccredentials"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/spiffe/go-spiffe/v2/svid/x509svid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/examples/helloworld/helloworld"
	"google.golang.org/grpc/peer"
)

type PKIOptions struct {
	LeafDNS   string
	LeafID    spiffeid.ID
	ClientDNS string
	ClientID  spiffeid.ID
}

type PKI struct {
	RootCertPEM   []byte
	RootCert      *x509.Certificate
	LeafCert      *x509.Certificate
	LeafCertPEM   []byte
	LeafPKPEM     []byte
	LeafPK        crypto.Signer
	ClientCertPEM []byte
	ClientCert    *x509.Certificate
	ClientPKPEM   []byte
	ClientPK      crypto.Signer

	leafID   spiffeid.ID
	clientID spiffeid.ID
}

func GenPKI(t *testing.T, opts PKIOptions) PKI {
	t.Helper()
	pki, err := GenPKIError(opts)
	require.NoError(t, err)
	return pki
}

func GenPKIError(opts PKIOptions) (PKI, error) {
	rootPK, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return PKI{}, err
	}

	rootCert := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Dapr Test Root CA"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	rootCertBytes, err := x509.CreateCertificate(rand.Reader, rootCert, rootCert, &rootPK.PublicKey, rootPK)
	if err != nil {
		return PKI{}, err
	}

	rootCert, err = x509.ParseCertificate(rootCertBytes)
	if err != nil {
		return PKI{}, err
	}

	rootCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootCertBytes})

	leafCertPEM, leafPKPEM, leafCert, leafPK, err := genLeafCert(rootPK, rootCert, opts.LeafID, opts.LeafDNS)
	if err != nil {
		return PKI{}, err
	}
	clientCertPEM, clientPKPEM, clientCert, clientPK, err := genLeafCert(rootPK, rootCert, opts.ClientID, opts.ClientDNS)
	if err != nil {
		return PKI{}, err
	}

	return PKI{
		RootCert:      rootCert,
		RootCertPEM:   rootCertPEM,
		LeafCertPEM:   leafCertPEM,
		LeafPKPEM:     leafPKPEM,
		LeafCert:      leafCert,
		LeafPK:        leafPK,
		ClientCertPEM: clientCertPEM,
		ClientPKPEM:   clientPKPEM,
		ClientCert:    clientCert,
		ClientPK:      clientPK,
		leafID:        opts.LeafID,
		clientID:      opts.ClientID,
	}, nil
}

func (p PKI) ClientGRPCCtx(t *testing.T) context.Context {
	t.Helper()

	bundle := x509bundle.New(spiffeid.RequireTrustDomainFromString("example.org"))
	bundle.AddX509Authority(p.RootCert)
	serverSVID := &mockSVID{
		bundle: bundle,
		svid: &x509svid.SVID{
			ID:           p.leafID,
			Certificates: []*x509.Certificate{p.LeafCert},
			PrivateKey:   p.LeafPK,
		},
	}

	clientSVID := &mockSVID{
		bundle: bundle,
		svid: &x509svid.SVID{
			ID:           p.clientID,
			Certificates: []*x509.Certificate{p.ClientCert},
			PrivateKey:   p.ClientPK,
		},
	}

	server := grpc.NewServer(grpc.Creds(grpccredentials.MTLSServerCredentials(serverSVID, serverSVID, tlsconfig.AuthorizeAny())))
	gs := new(greeterServer)
	helloworld.RegisterGreeterServer(server, gs)

	lis, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)

	go func() {
		server.Serve(lis)
	}()
	conn, err := grpc.DialContext(context.Background(), lis.Addr().String(),
		grpc.WithTransportCredentials(grpccredentials.MTLSClientCredentials(clientSVID, clientSVID, tlsconfig.AuthorizeAny())),
	)
	require.NoError(t, err)

	_, err = helloworld.NewGreeterClient(conn).SayHello(context.Background(), new(helloworld.HelloRequest))
	require.NoError(t, err)

	lis.Close()
	server.Stop()

	return gs.ctx
}

func genLeafCert(rootPK *ecdsa.PrivateKey, rootCert *x509.Certificate, id spiffeid.ID, dns string) ([]byte, []byte, *x509.Certificate, crypto.Signer, error) {
	pk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	pkBytes, err := x509.MarshalPKCS8PrivateKey(pk)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
			x509.ExtKeyUsageClientAuth,
		},
	}

	if len(dns) > 0 {
		cert.DNSNames = []string{dns}
	}

	if !id.IsZero() {
		cert.URIs = []*url.URL{id.URL()}
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, cert, rootCert, &pk.PublicKey, rootPK)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	cert, err = x509.ParseCertificate(certBytes)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	pkPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkBytes})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certBytes})

	return certPEM, pkPEM, cert, pk, nil
}

type mockSVID struct {
	svid   *x509svid.SVID
	bundle *x509bundle.Bundle
}

func (m *mockSVID) GetX509BundleForTrustDomain(_ spiffeid.TrustDomain) (*x509bundle.Bundle, error) {
	return m.bundle, nil
}

func (m *mockSVID) GetX509SVID() (*x509svid.SVID, error) {
	return m.svid, nil
}

type greeterServer struct {
	helloworld.UnimplementedGreeterServer
	ctx context.Context
}

func (s *greeterServer) SayHello(ctx context.Context, in *helloworld.HelloRequest) (*helloworld.HelloReply, error) {
	p, _ := peer.FromContext(ctx)
	s.ctx = peer.NewContext(context.Background(), p)
	return new(helloworld.HelloReply), nil
}
