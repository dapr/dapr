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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(rolesanywhere))
}

// rolesAnywhereComponent authenticates with AWS IAM Roles Anywhere, which
// signs requests with the workload's X.509 SVID key and only accepts ECDSA
// keys. The key check fails before any request is sent to AWS.
const rolesAnywhereComponent = `
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: s3
spec:
  type: bindings.aws.s3
  version: v1
  ignoreErrors: true
  metadata:
  - name: bucket
    value: dapr-test
  - name: region
    value: us-east-1
  - name: trustAnchorArn
    value: arn:aws:rolesanywhere:us-east-1:123456789012:trust-anchor/00000000-0000-0000-0000-000000000000
  - name: trustProfileArn
    value: arn:aws:rolesanywhere:us-east-1:123456789012:profile/00000000-0000-0000-0000-000000000000
  - name: assumeRoleArn
    value: arn:aws:iam::123456789012:role/dapr
`

// x509KeyTypeKey mirrors SpiffeProbeX509KeyTypeKey in
// cmd/daprd/components/state_spiffeprobe.go, which is behind a build tag.
const x509KeyTypeKey = "x509-key-type"

// rolesanywhere tests the workload key algorithm IAM Roles Anywhere needs.
type rolesanywhere struct {
	sentry    *sentry.Sentry
	placement *placement.Placement
	scheduler *scheduler.Scheduler

	defaultDaprd *daprd.Daprd
	defaultLog   *logline.LogLine
	ecdsaDaprd   *daprd.Daprd
}

func (r *rolesanywhere) Setup(t *testing.T) []framework.Option {
	r.sentry = sentry.New(t)
	bundle := r.sentry.CABundle()

	taFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(taFile, bundle.X509.TrustAnchors, 0o600))

	r.scheduler = scheduler.New(t,
		scheduler.WithSentry(r.sentry),
		scheduler.WithID("dapr-scheduler-server-0"),
	)
	r.placement = placement.New(t,
		placement.WithEnableTLS(true),
		placement.WithTrustAnchorsFile(taFile),
		placement.WithSentryAddress(r.sentry.Address()),
	)

	r.defaultLog = logline.New(t, logline.WithStdoutLineContains(
		"workload private key is ed25519.PrivateKey, but IAM Roles Anywhere requires ECDSA",
	))

	r.defaultDaprd = daprd.New(t,
		daprd.WithAppID("roles-anywhere-default"),
		daprd.WithMode("standalone"),
		daprd.WithExecOptions(
			exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(bundle.X509.TrustAnchors)),
			exec.WithStdout(r.defaultLog.Stdout()),
		),
		daprd.WithSentryAddress(r.sentry.Address()),
		daprd.WithPlacementAddresses(r.placement.Address()),
		daprd.WithSchedulerAddresses(r.scheduler.Address()),
		daprd.WithEnableMTLS(true),
		daprd.WithResourceFiles(rolesAnywhereComponent, spiffeProbeComponent),
	)

	r.ecdsaDaprd = daprd.New(t,
		daprd.WithAppID("roles-anywhere-ecdsa"),
		daprd.WithMode("standalone"),
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(bundle.X509.TrustAnchors))),
		daprd.WithSentryAddress(r.sentry.Address()),
		daprd.WithPlacementAddresses(r.placement.Address()),
		daprd.WithSchedulerAddresses(r.scheduler.Address()),
		daprd.WithEnableMTLS(true),
		daprd.WithWorkloadKeyAlgorithm("ecdsa"),
		daprd.WithResourceFiles(spiffeProbeComponent),
	)

	return []framework.Option{
		framework.WithProcesses(r.defaultLog, r.sentry, r.placement, r.scheduler, r.defaultDaprd, r.ecdsaDaprd),
	}
}

func (r *rolesanywhere) Run(t *testing.T, ctx context.Context) {
	r.sentry.WaitUntilRunning(t, ctx)
	r.placement.WaitUntilRunning(t, ctx)
	r.scheduler.WaitUntilRunning(t, ctx)
	r.defaultDaprd.WaitUntilRunning(t, ctx)
	r.ecdsaDaprd.WaitUntilRunning(t, ctx)

	keyType := func(t *testing.T, d *daprd.Daprd) string {
		t.Helper()
		resp, err := d.GRPCClient(t, ctx).GetState(ctx, &rtv1.GetStateRequest{
			StoreName: "spiffeprobe-store",
			Key:       x509KeyTypeKey,
		})
		require.NoError(t, err)
		return string(resp.GetData())
	}

	t.Run("the default Ed25519 workload key is rejected by IAM Roles Anywhere", func(t *testing.T) {
		assert.Equal(t, "ed25519.PrivateKey", keyType(t, r.defaultDaprd))
		r.defaultLog.EventuallyFoundAll(t)
	})

	t.Run("--workload-key-algorithm=ecdsa gives the workload an ECDSA key", func(t *testing.T) {
		assert.Equal(t, "*ecdsa.PrivateKey", keyType(t, r.ecdsaDaprd))
	})
}
