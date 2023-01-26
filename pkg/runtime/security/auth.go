package security

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"sync"
	"time"

	grpcMiddleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpcRetry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	daprCredentials "github.com/dapr/dapr/pkg/credentials"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
)

const (
	TLSServerName     = "cluster.local"
	sentrySignTimeout = time.Second * 5
	certType          = "CERTIFICATE"
	kubeTknPath       = "/var/run/secrets/dapr.io/sentrytoken/token"
	legacyKubeTknPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	sentryMaxRetries  = 100
)

type Authenticator interface {
	GetTrustAnchors() *x509.CertPool
	GetCurrentSignedCert() *SignedCertificate
	CreateSignedWorkloadCert(ctx context.Context, id, namespace, trustDomain string) (*SignedCertificate, error)
}

type authenticator struct {
	trustAnchors      *x509.CertPool
	certChainPem      []byte
	keyPem            []byte
	genCSRFunc        func(ctx context.Context, id string) ([]byte, []byte, error)
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

func newAuthenticator(sentryAddress string, trustAnchors *x509.CertPool, certChainPem, keyPem []byte, genCSRFunc func(ctx context.Context, id string) ([]byte, []byte, error)) Authenticator {
	return &authenticator{
		trustAnchors:  trustAnchors,
		certChainPem:  certChainPem,
		keyPem:        keyPem,
		genCSRFunc:    genCSRFunc,
		sentryAddress: sentryAddress,
		certMutex:     &sync.RWMutex{},
	}
}

// GetTrustAnchors returns the extracted root cert that serves as the trust anchor.
func (a *authenticator) GetTrustAnchors() *x509.CertPool {
	return a.trustAnchors
}

// GetCurrentSignedCert returns the current and latest signed certificate.
func (a *authenticator) GetCurrentSignedCert() *SignedCertificate {
	a.certMutex.RLock()
	defer a.certMutex.RUnlock()
	return a.currentSignedCert
}

// CreateSignedWorkloadCert returns a signed workload certificate, the PEM encoded private key
// And the duration of the signed cert.
func (a *authenticator) CreateSignedWorkloadCert(ctx context.Context, id, namespace, trustDomain string) (*SignedCertificate, error) {
	csrb, pkPem, err := a.genCSRFunc(ctx, id)
	if err != nil {
		return nil, err
	}
	certPem := pem.EncodeToMemory(&pem.Block{Type: certType, Bytes: csrb})

	config, err := daprCredentials.TLSConfigFromCertAndKey(a.certChainPem, a.keyPem, TLSServerName, a.trustAnchors)
	if err != nil {
		return nil, fmt.Errorf("failed to create tls config from cert and key: %w", err)
	}

	unaryClientInterceptor := grpcRetry.UnaryClientInterceptor()

	if diag.DefaultGRPCMonitoring.IsEnabled() {
		unaryClientInterceptor = grpcMiddleware.ChainUnaryClient(
			unaryClientInterceptor,
			diag.DefaultGRPCMonitoring.UnaryClientInterceptor(),
		)
	}

	conn, err := grpc.DialContext(ctx,
		a.sentryAddress,
		grpc.WithTransportCredentials(credentials.NewTLS(config)),
		grpc.WithUnaryInterceptor(unaryClientInterceptor))
	if err != nil {
		diag.DefaultMonitoring.MTLSWorkLoadCertRotationFailed(ctx, "sentry_conn")
		return nil, fmt.Errorf("error establishing connection to sentry: %w", err)
	}
	defer conn.Close()

	c := sentryv1pb.NewCAClient(conn)

	resp, err := c.SignCertificate(ctx,
		&sentryv1pb.SignCertificateRequest{
			CertificateSigningRequest: certPem,
			Id:                        getSentryIdentifier(id),
			Token:                     getToken(),
			TrustDomain:               trustDomain,
			Namespace:                 namespace,
		}, grpcRetry.WithMax(sentryMaxRetries), grpcRetry.WithPerRetryTimeout(sentrySignTimeout))
	if err != nil {
		diag.DefaultMonitoring.MTLSWorkLoadCertRotationFailed(ctx, "sign")
		return nil, fmt.Errorf("error from sentry SignCertificate: %w", err)
	}

	workloadCert := resp.GetWorkloadCertificate()
	validTimestamp := resp.GetValidUntil()
	if err = validTimestamp.CheckValid(); err != nil {
		diag.DefaultMonitoring.MTLSWorkLoadCertRotationFailed(ctx, "invalid_ts")
		return nil, fmt.Errorf("error parsing ValidUntil: %w", err)
	}

	expiry := validTimestamp.AsTime()
	trustChain := x509.NewCertPool()
	for _, c := range resp.GetTrustChainCertificates() {
		ok := trustChain.AppendCertsFromPEM(c)
		if !ok {
			diag.DefaultMonitoring.MTLSWorkLoadCertRotationFailed(ctx, "chaining")
			return nil, fmt.Errorf("failed adding trust chain cert to x509 CertPool: %w", err)
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

// currently we support Kubernetes identities.
func getToken() string {
	b, err := os.ReadFile(kubeTknPath)
	if err != nil && os.IsNotExist(err) {
		// Attempt to use the legacy token if that exists
		b, _ = os.ReadFile(legacyKubeTknPath)
		if len(b) > 0 {
			log.Warn("⚠️ daprd is initializing using the legacy service account token with access to Kubernetes APIs, which is discouraged. This usually happens when daprd is running against an older version of the Dapr control plane.")
		}
	}
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
