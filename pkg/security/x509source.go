/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package security

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	retry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
	"github.com/spiffe/go-spiffe/v2/bundle/x509bundle"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/spiffe/go-spiffe/v2/svid/x509svid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/modes"
	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/pkg/security/legacy"
	secpem "github.com/dapr/dapr/pkg/security/pem"
	sentryToken "github.com/dapr/dapr/pkg/security/token"
)

const (
	sentrySignTimeout = time.Second * 3
	sentryMaxRetries  = 5
)

type renewFn func(context.Context) (*x509.Certificate, error)

// x509source implements the go-spiffe x509 source interface.
// We use a custom source as our SPIFFE ID's come from the Sentry API and not
// the SPIFFE Workload API (SPIRE).
type x509source struct {
	currentSVID *x509svid.SVID

	// sentryAddress is the network address of the sentry server.
	sentryAddress string

	// sentryID is the SPIFFE ID of the sentry server which is validated when
	// request the identity document.
	sentryID spiffeid.ID

	// trustAnchors is the set of trusted root certificates of the dapr cluster.
	trustAnchors *x509bundle.Bundle

	// appID is the self selected APP ID of this Dapr instance.
	appID string

	// appNamespace is the dapr namespace this app belongs to.
	appNamespace string

	// kubernetesMode is true if Dapr is running in Kubernetes mode.
	kubernetesMode bool

	// requestFn is the function used to request the identity document from a
	// remote server. Used for overriding requesting from Sentry.
	requestFn RequestFn

	// trustAnchorSubscribers is a list of channels to notify when the trust
	// anchors are updated.
	trustAnchorSubscribers []chan<- struct{}

	// trustDomain is the optional trust domain which will be set when requesting
	// the identity certificate. Used by control plane services to request for
	// the control plane trust domain.
	trustDomain *string

	lock  sync.RWMutex
	clock clock.Clock
}

func newX509Source(ctx context.Context, clock clock.Clock, cptd spiffeid.TrustDomain, opts Options) (*x509source, error) {
	rootPEMs := opts.TrustAnchors

	if len(rootPEMs) == 0 {
		for {
			_, err := os.Stat(opts.TrustAnchorsFile)
			if err == nil {
				break
			}
			if !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}

			// Trust anchors file not be provided yet, wait.
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-clock.After(time.Second):
				log.Warnf("Trust anchors file '%s' not found, waiting...", opts.TrustAnchorsFile)
			}
		}

		log.Infof("Trust anchors file '%s' found", opts.TrustAnchorsFile)

		var err error
		rootPEMs, err = os.ReadFile(opts.TrustAnchorsFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read trust anchors file '%s': %w", opts.TrustAnchorsFile, err)
		}
	}

	trustAnchorCerts, err := secpem.DecodePEMCertificates(rootPEMs)
	if err != nil {
		return nil, fmt.Errorf("failed to decode trust anchors: %w", err)
	}

	sentryID, err := SentryID(cptd, opts.ControlPlaneNamespace)
	if err != nil {
		return nil, err
	}

	var trustDomain *string
	ns := CurrentNamespace()

	// If the service is a control plane service, set the trust domain to the
	// control plane trust domain.
	if isControlPlaneService(opts.AppID) && opts.ControlPlaneNamespace == ns {
		trustDomain = &opts.ControlPlaneTrustDomain
	}

	return &x509source{
		sentryAddress:  opts.SentryAddress,
		sentryID:       sentryID,
		trustAnchors:   x509bundle.FromX509Authorities(sentryID.TrustDomain(), trustAnchorCerts),
		appID:          opts.AppID,
		appNamespace:   ns,
		trustDomain:    trustDomain,
		kubernetesMode: opts.Mode == modes.KubernetesMode,
		requestFn:      opts.OverrideCertRequestSource,
		clock:          clock,
	}, nil
}

// GetX509SVID returns the current X.509 certificate identity as a SPIFFE SVID.
// Implements the go-spiffe x509 source interface.
func (x *x509source) GetX509SVID() (*x509svid.SVID, error) {
	x.lock.RLock()
	defer x.lock.RUnlock()
	return x.currentSVID, nil
}

// GetX509BundleForTrustDomain returns the static Trust Bundle for the Dapr
// cluster.
// Dapr does not support trust bundles for multiple trust domains.
// Implements the go-spiffe x509 bundle source interface.
func (x *x509source) GetX509BundleForTrustDomain(_ spiffeid.TrustDomain) (*x509bundle.Bundle, error) {
	x.lock.RLock()
	defer x.lock.RUnlock()
	return x.trustAnchors, nil
}

// startRotation starts up the manager responsible for renewing the workload
// certificate. Receives the initial certificate to calculate the next
// rotation time.
func (x *x509source) startRotation(ctx context.Context, fn renewFn, cert *x509.Certificate) {
	defer log.Debug("stopping workload cert expiry watcher")
	renewTime := renewalTime(cert.NotBefore, cert.NotAfter)
	log.Infof("Starting workload cert expiry watcher; current cert expires on: %s, renewing at %s",
		cert.NotAfter.String(), renewTime.String())

	for {
		select {
		case <-x.clock.After(renewTime.Sub(x.clock.Now())):
			log.Infof("Renewing workload cert; current cert expires on: %s", cert.NotAfter.String())
			newCert, err := fn(ctx)
			if err != nil {
				log.Errorf("Error renewing identity certificate, trying again in 10 seconds: %v", err)
				select {
				case <-x.clock.After(10 * time.Second):
					continue
				case <-ctx.Done():
					return
				}
			}
			cert = newCert
			renewTime = renewalTime(cert.NotBefore, cert.NotAfter)
			log.Infof("Successfully renewed workload cert; new cert expires on: %s", cert.NotAfter.String())

		case <-ctx.Done():
			return
		}
	}
}

// renewIdentityCertificate renews the identity certificate for the workload.
func (x *x509source) renewIdentityCertificate(ctx context.Context) (*x509.Certificate, error) {
	csrDER, pk, err := generateCSRAndPrivateKey(x.appID)
	if err != nil {
		return nil, err
	}

	workloadcert, err := x.requestFn(ctx, csrDER)
	if err != nil {
		return nil, err
	}

	if len(workloadcert) == 0 {
		return nil, errors.New("no certificates received from sentry")
	}

	spiffeID, err := x509svid.IDFromCert(workloadcert[0])
	if err != nil {
		return nil, fmt.Errorf("error parsing spiffe id from newly signed certificate: %w", err)
	}

	x.lock.Lock()
	defer x.lock.Unlock()
	x.currentSVID = &x509svid.SVID{
		ID:           spiffeID,
		Certificates: workloadcert,
		PrivateKey:   pk,
	}

	return workloadcert[0], nil
}

func generateCSRAndPrivateKey(id string) ([]byte, crypto.Signer, error) {
	if id == "" {
		return nil, nil, errors.New("id must not be empty")
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		diagnostics.DefaultMonitoring.MTLSInitFailed("prikeygen")
		return nil, nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	csrDER, err := x509.CreateCertificateRequest(rand.Reader,
		&x509.CertificateRequest{
			Subject:  pkix.Name{CommonName: id},
			DNSNames: []string{id},
		}, key)
	if err != nil {
		diagnostics.DefaultMonitoring.MTLSInitFailed("csr")
		return nil, nil, fmt.Errorf("failed to create sidecar csr: %w", err)
	}

	return csrDER, key, nil
}

func (x *x509source) requestFromSentry(ctx context.Context, csrDER []byte) ([]*x509.Certificate, error) {
	unaryClientInterceptor := retry.UnaryClientInterceptor(
		retry.WithMax(sentryMaxRetries),
		retry.WithPerRetryTimeout(sentrySignTimeout),
	)
	if diagnostics.DefaultGRPCMonitoring.IsEnabled() {
		unaryClientInterceptor = middleware.ChainUnaryClient(
			unaryClientInterceptor,
			diagnostics.DefaultGRPCMonitoring.UnaryClientInterceptor(),
		)
	}

	tlsConfig, err := legacy.NewDialClientOptionalClientAuth(x, x, tlsconfig.AuthorizeID(x.sentryID))
	if err != nil {
		return nil, fmt.Errorf("error creating tls config: %w", err)
	}

	conn, err := grpc.DialContext(ctx,
		x.sentryAddress,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
		grpc.WithUnaryInterceptor(unaryClientInterceptor),
		grpc.WithReturnConnectionError(),
	)
	if err != nil {
		diagnostics.DefaultMonitoring.MTLSWorkLoadCertRotationFailed("sentry_conn")
		return nil, fmt.Errorf("error establishing connection to sentry: %w", err)
	}

	defer conn.Close()

	token, tokenValidator, err := sentryToken.GetSentryToken(x.kubernetesMode)
	if err != nil {
		diagnostics.DefaultMonitoring.MTLSWorkLoadCertRotationFailed("sentry_token")
		return nil, fmt.Errorf("error obtaining token: %w", err)
	}

	req := &sentryv1pb.SignCertificateRequest{
		CertificateSigningRequest: pem.EncodeToMemory(&pem.Block{
			Type: "CERTIFICATE REQUEST", Bytes: csrDER,
		}),
		Id:             getSentryIdentifier(x.appID),
		Token:          token,
		Namespace:      x.appNamespace,
		TokenValidator: tokenValidator,
	}

	if x.trustDomain != nil {
		req.TrustDomain = *x.trustDomain
	} else {
		// For v1.11 sentry, if the trust domain is empty in the request then it
		// will return an empty certificate so we default to `public` here to
		// ensure we get an identity certificate back.
		// This request field is ignored for non control-plane requests in v1.12.
		// TODO: @joshvanl: Remove in v1.13.
		req.TrustDomain = "public"
	}

	resp, err := sentryv1pb.NewCAClient(conn).SignCertificate(ctx, req)
	if err != nil {
		diagnostics.DefaultMonitoring.MTLSWorkLoadCertRotationFailed("sign")
		return nil, fmt.Errorf("error from sentry SignCertificate: %w", err)
	}

	if err = resp.GetValidUntil().CheckValid(); err != nil {
		diagnostics.DefaultMonitoring.MTLSWorkLoadCertRotationFailed("invalid_ts")
		return nil, fmt.Errorf("error parsing ValidUntil: %w", err)
	}

	workloadcert, err := secpem.DecodePEMCertificates(resp.GetWorkloadCertificate())
	if err != nil {
		return nil, fmt.Errorf("error parsing newly signed certificate: %w", err)
	}

	return workloadcert, nil
}

func (x *x509source) updateTrustAnchorFromFile(ctx context.Context, filepath string) error {
	x.lock.RLock()
	defer x.lock.RUnlock()

	rootPEMs, err := os.ReadFile(filepath)
	if err != nil {
		return fmt.Errorf("failed to read trust anchors file '%s': %w", filepath, err)
	}

	trustAnchorCerts, err := secpem.DecodePEMCertificates(rootPEMs)
	if err != nil {
		return fmt.Errorf("failed to decode trust anchors: %w", err)
	}

	x.trustAnchors.SetX509Authorities(trustAnchorCerts)

	var wg sync.WaitGroup
	defer wg.Wait()

	wg.Add(len(x.trustAnchorSubscribers))
	for _, ch := range x.trustAnchorSubscribers {
		go func(chi chan<- struct{}) {
			defer wg.Done()
			select {
			case chi <- struct{}{}:
			case <-ctx.Done():
			}
		}(ch)
	}

	return nil
}

// renewalTime is 70% through the certificate validity period.
func renewalTime(notBefore, notAfter time.Time) time.Time {
	return notBefore.Add(notAfter.Sub(notBefore) * 7 / 10)
}

// isControlPlaneService returns true if the app ID corresponds to a Dapr
// control plane service.
func isControlPlaneService(id string) bool {
	switch id {
	case "dapr-operator",
		"dapr-placement",
		"dapr-injector",
		"dapr-sentry":
		return true
	default:
		return false
	}
}

func getSentryIdentifier(appID string) string {
	// return injected identity, default id if not present
	localID := os.Getenv("SENTRY_LOCAL_IDENTITY")
	if localID != "" {
		return localID
	}
	return appID
}
