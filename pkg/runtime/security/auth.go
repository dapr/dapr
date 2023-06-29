package security

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	grpcMiddleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpcRetry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	daprCredentials "github.com/dapr/dapr/pkg/credentials"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/pkg/runtime/security/consts"
)

const (
	TLSServerName     = "cluster.local"
	sentrySignTimeout = time.Second * 5
	kubeTknPath       = "/var/run/secrets/dapr.io/sentrytoken/token"
	legacyKubeTknPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	sentryMaxRetries  = 100

	jwksValidator       = "jwks"
	kubernetesValidator = "kubernetes"
)

type Authenticator interface {
	GetTrustAnchors() *x509.CertPool
	GetCurrentSignedCert() *SignedCertificate
	CreateSignedWorkloadCert(id, namespace, trustDomain string) (*SignedCertificate, error)
}

type authenticator struct {
	trustAnchors      *x509.CertPool
	certChainPem      []byte
	keyPem            []byte
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

func newAuthenticator(sentryAddress string, trustAnchors *x509.CertPool, certChainPem, keyPem []byte, genCSRFunc func(id string) ([]byte, []byte, error)) Authenticator {
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
func (a *authenticator) CreateSignedWorkloadCert(id, namespace, trustDomain string) (*SignedCertificate, error) {
	csrb, pkPem, err := a.genCSRFunc(id)
	if err != nil {
		return nil, err
	}
	certPem := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrb})

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

	conn, err := grpc.Dial(
		a.sentryAddress,
		grpc.WithTransportCredentials(credentials.NewTLS(config)),
		grpc.WithUnaryInterceptor(unaryClientInterceptor))
	if err != nil {
		diag.DefaultMonitoring.MTLSWorkLoadCertRotationFailed("sentry_conn")
		return nil, fmt.Errorf("error establishing connection to sentry: %w", err)
	}
	defer conn.Close()

	c := sentryv1pb.NewCAClient(conn)

	token, validatorName := getToken()
	tokenValidator := sentryv1pb.SignCertificateRequest_TokenValidator(sentryv1pb.SignCertificateRequest_TokenValidator_value[strings.ToUpper(validatorName)])
	resp, err := c.SignCertificate(context.Background(),
		&sentryv1pb.SignCertificateRequest{
			CertificateSigningRequest: certPem,
			Id:                        getSentryIdentifier(id),
			Token:                     token,
			TokenValidator:            tokenValidator,
			TrustDomain:               trustDomain,
			Namespace:                 namespace,
		}, grpcRetry.WithMax(sentryMaxRetries), grpcRetry.WithPerRetryTimeout(sentrySignTimeout))
	if err != nil {
		diag.DefaultMonitoring.MTLSWorkLoadCertRotationFailed("sign")
		return nil, fmt.Errorf("error from sentry SignCertificate: %w", err)
	}

	workloadCert := resp.GetWorkloadCertificate()
	validTimestamp := resp.GetValidUntil()
	if err = validTimestamp.CheckValid(); err != nil {
		diag.DefaultMonitoring.MTLSWorkLoadCertRotationFailed("invalid_ts")
		return nil, fmt.Errorf("error parsing ValidUntil: %w", err)
	}

	expiry := validTimestamp.AsTime()
	trustChain := x509.NewCertPool()
	for _, c := range resp.GetTrustChainCertificates() {
		ok := trustChain.AppendCertsFromPEM(c)
		if !ok {
			diag.DefaultMonitoring.MTLSWorkLoadCertRotationFailed("chaining")
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

// Returns the token for authenticating with Sentry.
func getToken() (token string, validator string) {
	// Check if we have a token in the DAPR_SENTRY_TOKEN env var (for the JWKS validator)
	if v := os.Getenv(consts.SentryTokenEnvVar); v != "" {
		log.Debug("Loaded token from DAPR_SENTRY_TOKEN environment variable")
		return v, jwksValidator
	}

	// Check if we have a token file in the DAPR_SENTRY_TOKEN_FILE env var (for the JWKS validator)
	if path := os.Getenv(consts.SentryTokenFileEnvVar); path != "" {
		// Attempt to read the file
		// Errors are logged but we still return the value even if empty
		b, err := os.ReadFile(path)
		if err != nil {
			log.Warnf("Failed to read token at path '%s': %v", path, err)
		} else {
			log.Debugf("Loaded token from path '%s' environment variable", path)
		}
		return string(b), jwksValidator
	}

	// Try to read a token from Kubernetes (for the default validator)
	b, err := os.ReadFile(kubeTknPath)
	if err != nil && os.IsNotExist(err) {
		// Attempt to use the legacy token if that exists
		b, _ = os.ReadFile(legacyKubeTknPath)
		if len(b) > 0 {
			log.Warn("⚠️ daprd is initializing using the legacy service account token with access to Kubernetes APIs, which is discouraged. This usually happens when daprd is running against an older version of the Dapr control plane.")
		}
	}
	if len(b) > 0 {
		return string(b), kubernetesValidator
	}

	return "", ""
}

func getSentryIdentifier(appID string) string {
	// return injected identity, default id if not present
	localID := os.Getenv(consts.SentryLocalIdentityEnvVar)
	if localID != "" {
		return localID
	}
	return appID
}
