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

package signing

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(certexpiry))
}

// certexpiry verifies that when the workload certificate TTL is very short (10
// seconds), the sidecar's SVID naturally rotates during a long-running
// workflow. Each rotation produces a new signing certificate in the state
// store, and the full signature chain must remain valid across rotations.
type certexpiry struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	daprd  *daprd.Daprd
	db     *sqlite.SQLite
}

func (c *certexpiry) Setup(t *testing.T) []framework.Option {
	// Configure sentry with a very short workload cert TTL (10s). The SPIFFE
	// library renews at 50% of the validity period, so renewal happens every ~5
	// seconds.
	c.sentry = sentry.New(t,
		sentry.WithConfiguration(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: sentryconfig
spec:
  mtls:
    workloadCertTTL: "10s"
    allowedClockSkew: "5s"
`),
	)

	c.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	c.place = placement.New(t, placement.WithSentry(t, c.sentry))
	c.sched = scheduler.New(t, scheduler.WithSentry(c.sentry), scheduler.WithID("dapr-scheduler-server-0"))

	c.daprd = daprd.New(t,
		daprd.WithSentry(t, c.sentry),
		daprd.WithPlacement(c.place),
		daprd.WithScheduler(c.sched),
		daprd.WithResourceFiles(c.db.GetComponent(t)),
		daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: sign-on
spec:
  features:
  - name: WorkflowHistorySigning
    enabled: true
`),
	)

	return []framework.Option{
		framework.WithProcesses(c.sentry, c.db, c.place, c.sched, c.daprd),
	}
}

func (c *certexpiry) Run(t *testing.T, ctx context.Context) {
	c.daprd.WaitUntilRunning(t, ctx)

	// The workflow waits for two external events with a gap in between so the
	// SVID rotates at least once. Any more events just add wall-clock time
	// without exercising new code paths.
	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("sign-certexpiry", func(ctx *dworkflow.WorkflowContext) (any, error) {
		if err := ctx.WaitForExternalEvent("event-0", time.Second*60).Await(nil); err != nil {
			return nil, err
		}
		if err := ctx.WaitForExternalEvent("event-1", time.Second*60).Await(nil); err != nil {
			return nil, err
		}
		return nil, nil
	})

	client := dworkflow.NewClient(c.daprd.GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "sign-certexpiry")
	require.NoError(t, err)

	meta, err := client.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, dworkflow.StatusRunning, meta.RuntimeStatus)

	require.NoError(t, client.RaiseEvent(ctx, id, "event-0"))
	require.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.GreaterOrEqual(col, fworkflow.CertificateCount(t, ctx, c.db, id), 1)
	}, 10*time.Second, 10*time.Millisecond)

	time.Sleep(7 * time.Second)

	require.NoError(t, client.RaiseEvent(ctx, id, "event-1"))

	meta, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, dworkflow.StatusCompleted, meta.RuntimeStatus)

	certCount := fworkflow.CertificateCount(t, ctx, c.db, id)
	assert.GreaterOrEqual(t, certCount, 2,
		"expected at least 2 certificates from natural SVID rotation, got %d", certCount)

	fworkflow.VerifySignatureChain(t, ctx, c.db, id, c.sentry.CABundle().X509.TrustAnchors)
	fworkflow.VerifyCertAppID(t, ctx, c.db, id, c.daprd.AppID())
}
