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

package sentry

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/bundle/x509bundle"
	"github.com/spiffe/go-spiffe/v2/spiffegrpc/grpccredentials"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	sentrypbv1 "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/tests/integration/framework/client"
	secpem "github.com/dapr/kit/crypto/pem"
)

// Rotation state keys persisted by sentry in the dapr-trust-bundle Secret in
// Kubernetes mode.
const (
	RotationPhaseSecretKey           = "rotation.phase"
	RotationNewCACertSecretKey       = "rotation.new-ca.crt"
	RotationNewIssCertSecretKey      = "rotation.new-issuer.crt"
	RotationNewIssKeySecretKey       = "rotation.new-issuer.key"
	RotationDistributedAtSecretKey   = "rotation.distributed-at"
	RotationSigningAtSecretKey       = "rotation.signing-at"
	RotationOldRootNotAfterSecretKey = "rotation.old-root-not-after"
)

// RotationSecretKeys is the full set of rotation state keys persisted by
// sentry in the dapr-trust-bundle Secret.
var RotationSecretKeys = []string{
	RotationPhaseSecretKey,
	RotationNewCACertSecretKey,
	RotationNewIssCertSecretKey,
	RotationNewIssKeySecretKey,
	RotationDistributedAtSecretKey,
	RotationSigningAtSecretKey,
	RotationOldRootNotAfterSecretKey,
}

// RotationState mirrors the on-disk rotation-state.json written by sentry in
// standalone mode.
type RotationState struct {
	Phase           string    `json:"phase"`
	DistributedAt   time.Time `json:"distributed_at"`
	SigningAt       time.Time `json:"signing_at"`
	OldRootNotAfter time.Time `json:"old_root_not_after"`
}

// RotationState loads rotation-state.json from the sentry's credentials
// directory, returning false if it does not exist or cannot be parsed yet.
func (s *Sentry) RotationState() (RotationState, bool) {
	data, err := os.ReadFile(filepath.Join(s.bundleDir, "rotation-state.json"))
	if err != nil {
		return RotationState{}, false
	}
	var state RotationState
	if json.Unmarshal(data, &state) != nil {
		return RotationState{}, false
	}
	return state, true
}

// DiskTrustAnchorsPEM returns the sentry's current on-disk trust anchors,
// i.e. what a workload watching the trust bundle would trust.
func (s *Sentry) DiskTrustAnchorsPEM(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(s.bundleDir, "ca.crt"))
	require.NoError(t, err)
	return data
}

// SignWorkloadCert asks sentry to sign a CSR with the insecure validator,
// trusting the given root CAs for the connection, and returns the issued leaf
// certificate, its issuer chain, and the trust anchors served in the
// response. It retries while sentry reloads its credentials, which happens
// via the file watcher whenever rotation writes files to disk.
func (s *Sentry) SignWorkloadCert(t *testing.T, ctx context.Context, anchorsPEM []byte) (leaf *x509.Certificate, chain, anchors []*x509.Certificate) {
	t.Helper()

	_, pk, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	csrDer, err := x509.CreateCertificateRequest(rand.Reader, new(x509.CertificateRequest), pk)
	require.NoError(t, err)
	csr := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDer})

	sentryID, err := spiffeid.FromString("spiffe://" + s.TrustDomain(t) + "/ns/" + s.Namespace() + "/dapr-sentry")
	require.NoError(t, err)
	x509bndl, err := x509bundle.Parse(sentryID.TrustDomain(), anchorsPEM)
	require.NoError(t, err)
	creds := grpccredentials.TLSClientCredentials(x509bndl, tlsconfig.AuthorizeID(sentryID))

	var resp *sentrypbv1.SignCertificateResponse
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		conn, cerr := grpc.NewClient(s.Address(), grpc.WithTransportCredentials(creds))
		if !assert.NoError(c, cerr) {
			return
		}
		defer conn.Close()

		rctx, cancel := context.WithTimeout(ctx, time.Second*5)
		defer cancel()
		var serr error
		resp, serr = sentrypbv1.NewCAClient(conn).SignCertificate(rctx, &sentrypbv1.SignCertificateRequest{
			Id:                        "myapp",
			Namespace:                 "default",
			CertificateSigningRequest: csr,
			TokenValidator:            sentrypbv1.SignCertificateRequest_INSECURE,
		})
		assert.NoError(c, serr)
	}, time.Second*20, time.Millisecond*10)

	workload := decodePEM(t, resp.GetWorkloadCertificate())
	require.NotEmpty(t, resp.GetTrustChainCertificates())
	anchors = decodePEM(t, resp.GetTrustChainCertificates()[0])

	return workload[0], workload[1:], anchors
}

// Metrics returns the raw metrics endpoint output of the sentry, or an error
// while the metrics server is unavailable, e.g. during a credentials reload.
func (s *Sentry) Metrics(t *testing.T, ctx context.Context) (string, error) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://localhost:%d/metrics", s.MetricsPort()), nil)
	require.NoError(t, err)
	resp, err := client.HTTP(t).Do(req)
	if err != nil {
		return "", err
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		resp.Body.Close()
		return "", err
	}
	return string(body), resp.Body.Close()
}

func decodePEM(t *testing.T, data []byte) []*x509.Certificate {
	t.Helper()
	certs, err := secpem.DecodePEMCertificates(data)
	require.NoError(t, err)
	require.NotEmpty(t, certs)
	return certs
}
