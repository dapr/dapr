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

package diagnostics_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"math/big"
	"net/url"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opencensus.io/stats/view"

	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/crypto/spiffe"
)

func TestMTLSMetricsProvider(t *testing.T) {
	for _, scenario := range []string{"startup", "init", "init failure", "rotation", "rotation failure", "disabled", "kubernetes", "cancelled", "invalid identity"} {
		t.Run(scenario, func(t *testing.T) {
			previous := diagnostics.DefaultMonitoring
			diagnostics.DefaultMonitoring = diagnostics.NewServiceMetricsForTest()
			t.Cleanup(func() { diagnostics.DefaultMonitoring = previous })
			meter := view.NewMeter()
			t.Cleanup(meter.Stop)
			if scenario != "startup" {
				require.NoError(t, diagnostics.InitMetrics(meter, "metrics-test", "default", config.MetricSpec{}))
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			_, key, err := ed25519.GenerateKey(rand.Reader)
			require.NoError(t, err)
			id := spiffeid.RequireFromString("spiffe://test.example.com/ns/default/app")
			ca := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
			caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, key.Public(), key)
			require.NoError(t, err)
			requests := 0
			opts := security.Options{
				AppID: "app", ControlPlaneTrustDomain: "test.example.com", ControlPlaneNamespace: "default",
				MTLSEnabled: scenario != "disabled", Healthz: healthz.New(),
				TrustAnchors: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
				OverrideCertRequestFn: func(ctx context.Context, csrDER []byte) (*spiffe.SVIDResponse, error) {
					requests++
					if scenario == "cancelled" {
						cancel()
						return nil, ctx.Err()
					}
					if scenario == "init failure" || (scenario == "rotation failure" && requests > 1) {
						return nil, errors.New("certificate request failed")
					}
					if scenario == "invalid identity" {
						return &spiffe.SVIDResponse{}, nil
					}
					csr, parseErr := x509.ParseCertificateRequest(csrDER)
					if parseErr != nil {
						return nil, parseErr
					}
					cert := &x509.Certificate{SerialNumber: big.NewInt(int64(requests + 1)), NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), URIs: []*url.URL{id.URL()}}
					// An overdue renewal triggers the real SPIFFE rotation loop immediately.
					if requests == 1 && (scenario == "rotation" || scenario == "rotation failure") {
						cert.NotAfter = time.Now().Add(-time.Second)
					}
					der, certErr := x509.CreateCertificate(rand.Reader, cert, ca, csr.PublicKey, key)
					if certErr != nil {
						return nil, certErr
					}
					signed, certErr := x509.ParseCertificate(der)
					if certErr != nil {
						return nil, certErr
					}
					return &spiffe.SVIDResponse{X509Certificates: []*x509.Certificate{signed}}, nil
				},
			}
			if scenario == "kubernetes" {
				t.Setenv("DAPR_UNSAFE_SKIP_CONTAINER_UID_GID_CHECK", "true")
				opts.Mode = modes.KubernetesMode
				opts.MTLSEnabled = false
			}
			p, err := security.New(ctx, opts)
			require.NoError(t, err)
			done := make(chan error, 1)
			go func() { done <- p.Run(ctx) }()
			if scenario == "init failure" || scenario == "cancelled" || scenario == "invalid identity" {
				select {
				case err = <-done:
					if scenario != "cancelled" {
						require.Error(t, err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("provider did not stop")
				}
			} else {
				handlerCtx, handlerCancel := context.WithTimeout(ctx, 5*time.Second)
				_, err = p.Handler(handlerCtx)
				handlerCancel()
				require.NoError(t, err)
			}
			if scenario == "startup" {
				cancel()
				select {
				case err = <-done:
					require.NoError(t, err)
				case <-time.After(5 * time.Second):
					t.Fatal("provider did not stop")
				}
				require.NoError(t, diagnostics.InitMetrics(meter, "metrics-test", "default", config.MetricSpec{}))
			}

			count := func(name string) int64 {
				rows, rerr := meter.RetrieveData("runtime/mtls/" + name)
				require.NoError(t, rerr)
				var n int64
				for _, row := range rows {
					n += row.Data.(*view.CountData).Value
				}
				return n
			}
			want := map[string]int64{"init_total": 0, "init_fail_total": 0, "workload_cert_rotated_total": 0, "workload_cert_rotated_fail_total": 0}
			switch scenario {
			case "startup", "init", "kubernetes":
				want["init_total"] = 1
			case "init failure", "invalid identity":
				want["init_fail_total"] = 1
			case "rotation":
				want["init_total"] = 1
				want["workload_cert_rotated_total"] = 1
			case "rotation failure":
				want["init_total"] = 1
				want["workload_cert_rotated_fail_total"] = 1
			}
			assert.Eventually(t, func() bool {
				for name, n := range want {
					if count(name) != n {
						return false
					}
				}
				return true
			}, 5*time.Second, 10*time.Millisecond, "missing mTLS outcome for %s", scenario)
			cancel()
			if scenario != "startup" && scenario != "init failure" && scenario != "cancelled" && scenario != "invalid identity" {
				select {
				case err = <-done:
					require.NoError(t, err)
				case <-time.After(5 * time.Second):
					t.Fatal("provider did not stop")
				}
			}

			for name, n := range want {
				assert.Equal(t, n, count(name), name)
			}
		})
	}
}
