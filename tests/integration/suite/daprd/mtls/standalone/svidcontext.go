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

package standalone

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(svidcontext))
}

// svidcontext validates end-to-end, through the real daprd binary, that the
// runtime injects the workload's SPIFFE identity (X.509 and JWT SVID sources)
// into the context of building-block component operations.
//
// It covers two distinct paths, which are wired independently:
//
//   - Data plane operations, via the `state.spiffeprobe` state store whose Get
//     reports which SVID sources were present in its operation context. Driven
//     through the normal state API so the request travels the real path:
//     gRPC server -> universal API -> resiliency runner -> component.
//   - Secret resolution, via the `secretstores.spiffeprobe` secret store which
//     records the sources present when the runtime resolved another component's
//     secretKeyRef. That happens while components are loading, so the probe
//     reports what it saw on a later read of a reserved key.
//
// Both probes are integration-only components compiled into the test daprd via
// the `state_spiffeprobe` and `secretstores_spiffeprobe` build tags (see
// tests/integration/framework/binary and cmd/daprd/components).
type svidcontext struct {
	mtlsDaprd  *daprd.Daprd
	plainDaprd *daprd.Daprd
	sentry     *sentry.Sentry
	placement  *placement.Placement
	scheduler  *scheduler.Scheduler
}

const spiffeProbeComponent = `apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: spiffeprobe-store
spec:
  type: state.spiffeprobe
  version: v1
`

//nolint:gosec // G101: component YAML, not a credential.
const spiffeProbeSecretStore = `apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: spiffeprobe-secrets
spec:
  type: secretstores.spiffeprobe
  version: v1
`

// secretRefComponent resolves a metadata value out of spiffeprobe-secrets. The
// runtime calls that store's GetSecret while loading this component, before its
// Init, which is the context the probe records.
//
//nolint:gosec // G101: component YAML, not a credential.
const secretRefComponent = `apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: secretref-store
spec:
  type: state.in-memory
  version: v1
  metadata:
  - name: probe
    secretKeyRef:
      name: probe-secret
auth:
  secretStore: spiffeprobe-secrets
`

// resolutionReportKey mirrors SpiffeProbeResolutionReportKey in
// cmd/daprd/components/secretstores_spiffeprobe.go. It is duplicated rather
// than imported because every file in that package is behind a build tag, so
// the package has no Go files from the integration suite's point of view.
const resolutionReportKey = "__resolution__"

func (s *svidcontext) Setup(t *testing.T) []framework.Option {
	s.sentry = sentry.New(t)
	bundle := s.sentry.CABundle()

	taFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(taFile, bundle.X509.TrustAnchors, 0o600))

	s.scheduler = scheduler.New(t,
		scheduler.WithSentry(s.sentry),
		scheduler.WithID("dapr-scheduler-server-0"),
	)

	s.placement = placement.New(t,
		placement.WithEnableTLS(true),
		placement.WithTrustAnchorsFile(taFile),
		placement.WithSentryAddress(s.sentry.Address()),
	)

	// mTLS enabled: the workload has a SPIFFE identity, so component operations
	// should carry the X.509 and JWT SVID sources.
	s.mtlsDaprd = daprd.New(t,
		daprd.WithAppID("mtls-app"),
		daprd.WithMode("standalone"),
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(bundle.X509.TrustAnchors))),
		daprd.WithSentryAddress(s.sentry.Address()),
		daprd.WithPlacementAddresses(s.placement.Address()),
		daprd.WithSchedulerAddresses(s.scheduler.Address()),
		daprd.WithEnableMTLS(true),
		daprd.WithResourceFiles(spiffeProbeComponent, spiffeProbeSecretStore, secretRefComponent),
	)

	// mTLS disabled: WithSVIDContext is a no-op, so component operations must not
	// carry any SVID source. This is the negative control proving the positive
	// assertion is not vacuous.
	s.plainDaprd = daprd.New(t,
		daprd.WithAppID("plain-app"),
		daprd.WithResourceFiles(spiffeProbeComponent, spiffeProbeSecretStore, secretRefComponent),
	)

	return []framework.Option{
		framework.WithProcesses(s.sentry, s.placement, s.scheduler, s.mtlsDaprd, s.plainDaprd),
	}
}

func (s *svidcontext) Run(t *testing.T, ctx context.Context) {
	s.sentry.WaitUntilRunning(t, ctx)
	s.placement.WaitUntilRunning(t, ctx)
	s.scheduler.WaitUntilRunning(t, ctx)
	s.mtlsDaprd.WaitUntilRunning(t, ctx)
	s.plainDaprd.WaitUntilRunning(t, ctx)

	// probe reads back which SVID sources the component saw in its Get context.
	probe := func(t *testing.T, d *daprd.Daprd) map[string]bool {
		t.Helper()
		resp, err := d.GRPCClient(t, ctx).GetState(ctx, &rtv1.GetStateRequest{
			StoreName: "spiffeprobe-store",
			Key:       "svid",
		})
		require.NoError(t, err)
		var got map[string]bool
		require.NoError(t, json.Unmarshal(resp.GetData(), &got))
		return got
	}

	// resolutionProbe reads back which SVID sources the secret store saw while
	// the runtime resolved secretref-store's secretKeyRef. The referencing
	// component is parked until its secret store loads, so wait for the probe to
	// have been called before reading the report.
	resolutionProbe := func(t *testing.T, d *daprd.Daprd) map[string]string {
		t.Helper()
		client := d.GRPCClient(t, ctx)
		report := func() (map[string]string, error) {
			resp, err := client.GetSecret(ctx, &rtv1.GetSecretRequest{
				StoreName: "spiffeprobe-secrets",
				Key:       resolutionReportKey,
			})
			if err != nil {
				return nil, err
			}
			return resp.GetData(), nil
		}

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			data, err := report()
			if !assert.NoError(c, err) {
				return
			}
			assert.Equal(c, "true", data["recorded"],
				"secret resolution has not called the probe secret store yet")
		}, time.Second*15, time.Millisecond*100)

		// Recorded once during load and never rewritten, so a plain read now is
		// stable.
		data, err := report()
		require.NoError(t, err)
		return data
	}

	t.Run("with mTLS the component sees the SVID sources", func(t *testing.T) {
		got := probe(t, s.mtlsDaprd)
		assert.True(t, got["x509"], "X.509 SVID source should be in the component operation context")
		assert.True(t, got["jwt"], "JWT SVID source should be in the component operation context")
	})

	t.Run("without mTLS the component sees no SVID sources", func(t *testing.T) {
		got := probe(t, s.plainDaprd)
		assert.False(t, got["x509"], "X.509 SVID source should be absent when mTLS is disabled")
		assert.False(t, got["jwt"], "JWT SVID source should be absent when mTLS is disabled")
	})

	t.Run("with mTLS the secret store sees the SVID sources during secret resolution", func(t *testing.T) {
		got := resolutionProbe(t, s.mtlsDaprd)
		assert.Equal(t, "true", got["x509"], "X.509 SVID source should be in the secret resolution context")
		assert.Equal(t, "true", got["jwt"], "JWT SVID source should be in the secret resolution context")
	})

	t.Run("without mTLS the secret store sees no SVID sources during secret resolution", func(t *testing.T) {
		got := resolutionProbe(t, s.plainDaprd)
		assert.Equal(t, "false", got["x509"], "X.509 SVID source should be absent when mTLS is disabled")
		assert.Equal(t, "false", got["jwt"], "JWT SVID source should be absent when mTLS is disabled")
	})
}
