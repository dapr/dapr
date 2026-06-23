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
// It relies on the `state.spiffeprobe` state store, an integration-only
// component compiled into the test daprd via the `state_spiffeprobe` build tag
// (see tests/integration/framework/binary and cmd/daprd/components), whose Get
// reports which SVID sources were present in its operation context. We drive it
// through the normal state API so the request travels the real path:
// gRPC server -> universal API -> resiliency runner -> component.
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
		daprd.WithResourceFiles(spiffeProbeComponent),
	)

	// mTLS disabled: WithSVIDContext is a no-op, so component operations must not
	// carry any SVID source. This is the negative control proving the positive
	// assertion is not vacuous.
	s.plainDaprd = daprd.New(t,
		daprd.WithAppID("plain-app"),
		daprd.WithResourceFiles(spiffeProbeComponent),
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
}
