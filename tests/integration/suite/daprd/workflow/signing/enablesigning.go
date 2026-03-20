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
	suite.Register(new(enablesigning))
}

// enablesigning verifies that when a workflow starts on a non-signing host
// then moves to a signing host, catch-up signatures cover the previously
// unsigned events.
type enablesigning struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	db     *sqlite.SQLite
	daprd1 *daprd.Daprd
}

func (e *enablesigning) Setup(t *testing.T) []framework.Option {
	e.sentry = sentry.New(t)
	e.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	e.place = placement.New(t, placement.WithSentry(t, e.sentry))
	e.sched = scheduler.New(t, scheduler.WithSentry(e.sentry))

	e.daprd1 = daprd.New(t,
		daprd.WithSentry(t, e.sentry),
		daprd.WithPlacementAddresses(e.place.Address()),
		daprd.WithScheduler(e.sched),
		daprd.WithResourceFiles(e.db.GetComponent(t)),
		daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: signoff
spec:
  features:
  - name: WorkflowSignState
    enabled: false
`),
	)

	return []framework.Option{
		framework.WithProcesses(e.sentry, e.db, e.place, e.sched, e.daprd1),
	}
}

func (e *enablesigning) Run(t *testing.T, ctx context.Context) {
	e.daprd1.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("sign-enable", func(ctx *dworkflow.WorkflowContext) (any, error) {
		var payload string
		if err := ctx.WaitForExternalEvent("continue", time.Second*5).Await(&payload); err != nil {
			return nil, err
		}
		return payload, nil
	})

	client1 := dworkflow.NewClient(e.daprd1.GRPCConn(t, ctx))
	require.NoError(t, client1.StartWorker(ctx, reg))

	id, err := client1.ScheduleWorkflow(ctx, "sign-enable")
	require.NoError(t, err)

	meta, err := client1.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, dworkflow.StatusRunning, meta.RuntimeStatus)
	assert.Equal(t, 0, e.db.CountStateKeys(t, ctx, "signature"))

	e.daprd1.Kill(t)

	daprd2 := daprd.New(t,
		daprd.WithSentry(t, e.sentry),
		daprd.WithAppID(e.daprd1.AppID()),
		daprd.WithPlacementAddresses(e.place.Address()),
		daprd.WithScheduler(e.sched),
		daprd.WithResourceFiles(e.db.GetComponent(t)),
	)
	daprd2.Run(t, ctx)
	t.Cleanup(func() { daprd2.Cleanup(t) })
	daprd2.WaitUntilRunning(t, ctx)

	client2 := dworkflow.NewClient(daprd2.GRPCConn(t, ctx))
	require.NoError(t, client2.StartWorker(ctx, reg))

	require.NoError(t, client2.RaiseEvent(ctx, id, "continue",
		dworkflow.WithEventPayload("resumed")))

	_, err = client2.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	fworkflow.VerifySignatureChain(t, ctx, e.db, id,
		e.sentry.CABundle().X509.TrustAnchors,
	)
	fworkflow.VerifyCertAppID(t, ctx, e.db, id, daprd2.AppID())
}
