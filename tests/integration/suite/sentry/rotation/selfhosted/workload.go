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

package selfhosted

import (
	"context"
	"fmt"
	nethttp "net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	secpem "github.com/dapr/kit/crypto/pem"
)

func init() {
	suite.Register(new(workload))
}

// workload ensures mTLS service invocation between two daprds keeps working
// through a full root CA rotation. The daprds source their trust anchors from
// a watched file (DAPR_TRUST_ANCHORS_FILE) — sentry's on-disk trust bundle —
// so they pick up the combined and then rotated root CAs live, exactly as
// pods mounting the trust-bundle ConfigMap do in Kubernetes mode.
type workload struct {
	sentry *procsentry.Sentry
	server *prochttp.HTTP
	daprdA *procdaprd.Daprd
	daprdB *procdaprd.Daprd
}

func (w *workload) Setup(t *testing.T) []framework.Option {
	// The root CA expires 30s in, so the full rotation cycle — distributing,
	// signing (~3s, propagation window), cleanup (~30s, once the old root and
	// the workload cert grace period expire) — runs while the daprds are
	// serving. Their certs (3s TTL) renew continuously, rolling onto the new
	// issuer after the signing switch.
	bndl := genBundle(t, time.Second*30)
	w.sentry = procsentry.New(t,
		procsentry.WithTrustDomain(trustDomain),
		procsentry.WithCABundle(bndl),
		procsentry.WithRotationCheckInterval(time.Second),
		procsentry.WithRotationPropagationWindow(time.Second*3),
		procsentry.WithConfiguration(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: sentryconfig
spec:
  mtls:
    workloadCertTTL: "3s"
    allowedClockSkew: "1s"
`),
	)

	// Sentry rewrites ca.crt in its credentials directory as rotation
	// progresses; the daprds watch that exact file for their trust anchors.
	taFile := filepath.Join(w.sentry.BundleDirectory(), "ca.crt")

	w.server = prochttp.New(t, prochttp.WithHandlerFunc("/hello", func(rw nethttp.ResponseWriter, _ *nethttp.Request) {
		rw.WriteHeader(nethttp.StatusOK)
	}))

	w.daprdA = procdaprd.New(t,
		procdaprd.WithSentryTrustAnchorsFile(t, w.sentry, taFile),
		procdaprd.WithControlPlaneTrustDomain(trustDomain),
	)
	w.daprdB = procdaprd.New(t,
		procdaprd.WithSentryTrustAnchorsFile(t, w.sentry, taFile),
		procdaprd.WithControlPlaneTrustDomain(trustDomain),
		procdaprd.WithAppPort(w.server.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(w.sentry, w.server, w.daprdA, w.daprdB),
	}
}

func (w *workload) Run(t *testing.T, ctx context.Context) {
	w.sentry.WaitUntilRunning(t, ctx)
	w.daprdA.WaitUntilRunning(t, ctx)
	w.daprdB.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)
	invoke := func(c *assert.CollectT) {
		url := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/hello", w.daprdA.HTTPPort(), w.daprdB.AppID())
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, url, nil)
		if !assert.NoError(c, err) {
			return
		}
		resp, err := httpClient.Do(req)
		if !assert.NoError(c, err) {
			return
		}
		assert.NoError(c, resp.Body.Close())
		assert.Equal(c, nethttp.StatusOK, resp.StatusCode)
	}

	// Invocation works before the rotation completes (the rotation may already
	// be distributing — both roots are trusted throughout).
	require.EventuallyWithT(t, invoke, time.Second*20, time.Millisecond*100)

	// Wait for the full rotation cycle to complete: rotation state cleared and
	// only the new root CA left in the trust anchors.
	dir := w.sentry.BundleDirectory()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, ok := readRotationState(dir)
		if !assert.False(c, ok, "rotation must complete") {
			return
		}
		anchors, err := secpem.DecodePEMCertificates(diskAnchorsPEM(t, dir))
		if assert.NoError(c, err) {
			assert.Len(c, anchors, 1, "only the new root CA must remain")
		}
	}, time.Second*60, time.Millisecond*500)

	// The old root CA has been rotated out (and has expired); invocation still
	// works because both daprds picked up the new trust anchors from the
	// watched file and renewed their certs against the new issuer.
	require.EventuallyWithT(t, invoke, time.Second*30, time.Millisecond*500)
}
