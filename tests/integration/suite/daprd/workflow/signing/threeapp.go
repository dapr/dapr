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
	suite.Register(new(threeapp))
}

// threeapp verifies that a workflow spanning three daprd instances (app A
// orchestrates, calling activities that may be routed to app B or app C)
// produces valid signatures with certificates attributed to the orchestrator.
type threeapp struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	daprd3 *daprd.Daprd
	db     *sqlite.SQLite
}

func (a *threeapp) Setup(t *testing.T) []framework.Option {
	a.sentry = sentry.New(t)
	a.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	a.place = placement.New(t, placement.WithSentry(t, a.sentry))
	a.sched = scheduler.New(t, scheduler.WithSentry(a.sentry), scheduler.WithID("dapr-scheduler-server-0"))

	a.daprd1 = daprd.New(t,
		daprd.WithAppID("sign-three-a"),
		daprd.WithSentry(t, a.sentry),
		daprd.WithPlacementAddresses(a.place.Address()),
		daprd.WithScheduler(a.sched),
		daprd.WithResourceFiles(a.db.GetComponent(t)),
		daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: sign-on
spec:
  features:
  - name: WorkflowSignState
    enabled: true
`),
	)

	a.daprd2 = daprd.New(t,
		daprd.WithAppID("sign-three-b"),
		daprd.WithSentry(t, a.sentry),
		daprd.WithPlacementAddresses(a.place.Address()),
		daprd.WithScheduler(a.sched),
		daprd.WithResourceFiles(a.db.GetComponent(t)),
		daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: sign-on
spec:
  features:
  - name: WorkflowSignState
    enabled: true
`),
	)

	a.daprd3 = daprd.New(t,
		daprd.WithAppID("sign-three-c"),
		daprd.WithSentry(t, a.sentry),
		daprd.WithPlacementAddresses(a.place.Address()),
		daprd.WithScheduler(a.sched),
		daprd.WithResourceFiles(a.db.GetComponent(t)),
		daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: sign-on
spec:
  features:
  - name: WorkflowSignState
    enabled: true
`),
	)

	return []framework.Option{
		framework.WithProcesses(a.sentry, a.db, a.place, a.sched, a.daprd1, a.daprd2, a.daprd3),
	}
}

func (a *threeapp) Run(t *testing.T, ctx context.Context) {
	a.daprd1.WaitUntilRunning(t, ctx)
	a.daprd2.WaitUntilRunning(t, ctx)
	a.daprd3.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("sign-chain", func(ctx *dworkflow.WorkflowContext) (any, error) {
		var b, c string
		if err := ctx.CallActivity("step-b").Await(&b); err != nil {
			return nil, err
		}
		if err := ctx.CallActivity("step-c").Await(&c); err != nil {
			return nil, err
		}
		return b + "+" + c, nil
	})
	reg.AddActivityN("step-b", func(ctx dworkflow.ActivityContext) (any, error) {
		return "from-b", nil
	})
	reg.AddActivityN("step-c", func(ctx dworkflow.ActivityContext) (any, error) {
		return "from-c", nil
	})

	// All three instances register the same workflow and activities so that
	// placement can route activities across the cluster.
	client1 := dworkflow.NewClient(a.daprd1.GRPCConn(t, ctx))
	require.NoError(t, client1.StartWorker(ctx, reg))

	client2 := dworkflow.NewClient(a.daprd2.GRPCConn(t, ctx))
	require.NoError(t, client2.StartWorker(ctx, reg))

	client3 := dworkflow.NewClient(a.daprd3.GRPCConn(t, ctx))
	require.NoError(t, client3.StartWorker(ctx, reg))

	// Schedule on the orchestrator instance (app A).
	id, err := client1.ScheduleWorkflow(ctx, "sign-chain")
	require.NoError(t, err)

	meta, err := client1.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, dworkflow.StatusCompleted, meta.RuntimeStatus)

	assert.Positive(t, fworkflow.SignatureCount(t, ctx, a.db, id))
	assert.GreaterOrEqual(t, fworkflow.CertificateCount(t, ctx, a.db, id), 1)

	fworkflow.VerifySignatureChain(t, ctx, a.db, id, a.sentry.CABundle().X509.TrustAnchors)
	fworkflow.VerifyCertAppID(t, ctx, a.db, id, "sign-three-a")
}
