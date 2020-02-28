package security

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"os"
	"sync"
	"time"

	pb "github.com/dapr/dapr/pkg/proto/sentry"
	"github.com/golang/protobuf/ptypes"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const (
	serverName        = "cluster.local"
	sentrySignTimeout = time.Second * 5
	certType          = "CERTIFICATE"
	kubeTknPath       = "/var/run/secrets/kubernetes.io/serviceaccount/token"
)

type Authenticator interface {
	GetTrustAnchors() *x509.CertPool
	GetCurrentSignedCert() *SignedCertificate
	CreateSignedWorkloadCert(id string) (*SignedCertificate, error)
}

type authenticator struct {
	trustAnchors      *x509.CertPool
	genCSRFunc        func(id string) ([]byte, []byte, error)
	sentryAddress     string
	currentSignedCert *SignedCertificate
	certMutex         *sync.RWMutex
}

type SignedCertificate struct {
	WorkloadCert  []byte
	PrivateKeyPem []byte
	Expiry        time.Time
	TrustChain    *x509.CertPool
}

func newAuthenticator(sentryAddress string, trustAnchors *x509.CertPool, genCSRFunc func(id string) ([]byte, []byte, error)) Authenticator {
	return &authenticator{
		trustAnchors:  trustAnchors,
		genCSRFunc:    genCSRFunc,
		sentryAddress: sentryAddress,
		certMutex:     &sync.RWMutex{},
	}
}

// GetTrustAnchors returns the extracted root cert that serves as the trust anchor.
func (a *authenticator) GetTrustAnchors() *x509.CertPool {
	return a.trustAnchors
}

// GetCurrentSignedCert returns the current and latest signed certificate
func (a *authenticator) GetCurrentSignedCert() *SignedCertificate {
	a.certMutex.RLock()
	defer a.certMutex.RUnlock()
	return a.currentSignedCert
}

// CreateSignedWorkloadCert returns a signed workload certificate, the PEM encoded private key
// And the duration of the signed cert.
func (a *authenticator) CreateSignedWorkloadCert(id string) (*SignedCertificate, error) {
	csrb, pkPem, err := a.genCSRFunc(id)
	if err != nil {
		return nil, err
	}
	certPem := pem.EncodeToMemory(&pem.Block{Type: certType, Bytes: csrb})

	config := &tls.Config{
		InsecureSkipVerify: false,
		RootCAs:            a.trustAnchors,
		ServerName:         serverName,
	}
	conn, err := grpc.Dial(a.sentryAddress, grpc.WithTransportCredentials(credentials.NewTLS(config)))
	if err != nil {
		return nil, fmt.Errorf("error establishing connection to sentry: %s", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), sentrySignTimeout)
	defer cancel()

	c := pb.NewCAClient(conn)
	resp, err := c.SignCertificate(ctx, &pb.SignCertificateRequest{
		CertificateSigningRequest: certPem,
		Id:                        getSentryIdentifier(id),
		Token:                     getToken(),
	})
	if err != nil {
		return nil, fmt.Errorf("error from sentry SignCertificate: %s", err)
	}

	workloadCert := resp.GetWorkloadCertificate()
	validTimestamp := resp.GetValidUntil()
	expiry, err := ptypes.Timestamp(validTimestamp)
	if err != nil {
		return nil, fmt.Errorf("error parsing ValidUntil: %s", err)
	}

	trustChain := x509.NewCertPool()
	for _, c := range resp.GetTrustChainCertificates() {
		ok := trustChain.AppendCertsFromPEM(c)
		if !ok {
			return nil, fmt.Errorf("failed adding trust chain cert to x509 CertPool: %s", err)
		}
	}

	signedCert := &SignedCertificate{
		WorkloadCert:  workloadCert,
		PrivateKeyPem: pkPem,
		Expiry:        expiry,
		TrustChain:    trustChain,
	}

	a.certMutex.Lock()
	defer a.certMutex.Unlock()

	a.currentSignedCert = signedCert
	return signedCert, nil
}

// currently we support Kubernetes identities
func getToken() string {
	b, _ := ioutil.ReadFile(kubeTknPath)
	return string(b)
}

func getSentryIdentifier(appID string) string {
	// return injected identity, default id if not present
	localID := os.Getenv("SENTRY_LOCAL_IDENTITY")
	if localID != "" {
		return localID
	}
	return appID
}
