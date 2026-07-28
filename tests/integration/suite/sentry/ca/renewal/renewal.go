/*
Copyright 2026 The Dapr Authors
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

package renewal

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/bundle/x509bundle"
	"github.com/spiffe/go-spiffe/v2/spiffegrpc/grpccredentials"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	sentrypbv1 "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	cryptopem "github.com/dapr/kit/crypto/pem"
)

// genBundle generates a full CA bundle with the given X.509 CA TTL.
func genBundle(t *testing.T, trustDomain string, caTTL time.Duration) bundle.Bundle {
	t.Helper()

	_, rootKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	jwtKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	x509bundle, err := bundle.GenerateX509(bundle.OptionsX509{
		X509RootKey:      rootKey,
		TrustDomain:      trustDomain,
		AllowedClockSkew: time.Second * 5,
		OverrideCATTL:    &caTTL,
	})
	require.NoError(t, err)
	jwtbundle, err := bundle.GenerateJWT(bundle.OptionsJWT{
		JWTRootKey:  jwtKey,
		TrustDomain: trustDomain,
	})
	require.NoError(t, err)

	return bundle.Bundle{X509: x509bundle, JWT: jwtbundle}
}

// signCert requests a workload certificate from sentry and returns the leaf
// certificate.
func signCert(t require.TestingT, ctx context.Context, client sentrypbv1.CAClient, appID, namespace string) (*x509.Certificate, error) {
	_, pk, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	csrDer, err := x509.CreateCertificateRequest(rand.Reader, new(x509.CertificateRequest), pk)
	require.NoError(t, err)

	rctx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()
	resp, err := client.SignCertificate(rctx, &sentrypbv1.SignCertificateRequest{
		Id:                        appID,
		Namespace:                 namespace,
		CertificateSigningRequest: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDer}),
		TokenValidator:            sentrypbv1.SignCertificateRequest_INSECURE,
	})
	if err != nil {
		return nil, err
	}

	certs, err := cryptopem.DecodePEMCertificates(resp.GetWorkloadCertificate())
	if err != nil {
		return nil, err
	}

	return certs[0], nil
}

// parseAnchors decodes all certificates in a PEM trust anchors bundle.
func parseAnchors(t require.TestingT, anchorsPEM []byte) []*x509.Certificate {
	certs, err := cryptopem.DecodePEMCertificates(anchorsPEM)
	require.NoError(t, err)
	return certs
}

// chainsTo reports whether the leaf certificate was signed by the given
// issuer certificate.
func chainsTo(leaf, issuer *x509.Certificate) bool {
	return leaf.CheckSignatureFrom(issuer) == nil
}

// dialSentry dials sentry verifying its serving certificate against the given
// trust anchors, returning an error rather than failing the test so callers
// can retry around server restarts.
func dialSentry(ctx context.Context, port int, anchorsPEM []byte, sentryID string) (sentrypbv1.CAClient, func() error, error) {
	sentrySPIFFEID, err := spiffeid.FromString(sentryID)
	if err != nil {
		return nil, nil, err
	}

	x509bndl, err := x509bundle.Parse(sentrySPIFFEID.TrustDomain(), anchorsPEM)
	if err != nil {
		return nil, nil, err
	}
	creds := grpccredentials.TLSClientCredentials(x509bndl, tlsconfig.AuthorizeID(sentrySPIFFEID))

	dctx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()
	//nolint:staticcheck
	conn, err := grpc.DialContext(dctx, fmt.Sprintf("127.0.0.1:%d", port),
		grpc.WithTransportCredentials(creds),
		grpc.WithReturnConnectionError(), //nolint:staticcheck
		grpc.WithBlock(),                 //nolint:staticcheck
	)
	if err != nil {
		return nil, nil, err
	}

	return sentrypbv1.NewCAClient(conn), conn.Close, nil
}

// poolOf builds a certificate pool from a PEM trust anchors bundle.
func poolOf(t require.TestingT, anchorsPEM []byte) *x509.CertPool {
	pool := x509.NewCertPool()
	for _, cert := range parseAnchors(t, anchorsPEM) {
		pool.AddCert(cert)
	}
	return pool
}

// x509CertVerifyOptions returns verify options with the given roots and a
// single intermediate, accepting any key usage.
func x509CertVerifyOptions(roots *x509.CertPool, intermediate *x509.Certificate) x509.VerifyOptions {
	ints := x509.NewCertPool()
	ints.AddCert(intermediate)
	return x509.VerifyOptions{
		Roots:         roots,
		Intermediates: ints,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}
}

// configuration is the daprsystem configuration used by the standalone
// renewal tests. It keeps the allowed clock skew small so the short test
// certificate lifetimes are not swallowed by the skew backdating (the default
// skew is 15 minutes, far longer than the test CA TTLs).
const configuration = `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: daprsystem
spec:
  mtls:
    allowedClockSkew: 1s
`

// metricsSnapshot fetches sentry's Prometheus metrics endpoint and returns
// the untagged single-value metrics keyed by name.
func metricsSnapshot(ctx context.Context, metricsPort int) (map[string]float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://localhost:%d/metrics", metricsPort), nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	metrics := make(map[string]float64)
	for line := range strings.SplitSeq(string(body), "\n") {
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		split := strings.Split(line, " ")
		if len(split) != 2 {
			continue
		}
		value, verr := strconv.ParseFloat(split[1], 64)
		if verr != nil {
			continue
		}
		metrics[split[0]] = value
	}

	return metrics, nil
}
