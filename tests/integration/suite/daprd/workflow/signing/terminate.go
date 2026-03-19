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
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(terminate))
}

// terminate verifies that a workflow which is terminated mid-execution still
// has valid signatures recorded in the state store for the events that were
// processed before termination.
type terminate struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	daprd  *daprd.Daprd
	db     *sqlite.SQLite
}

func (tr *terminate) Setup(t *testing.T) []framework.Option {
	tr.sentry = sentry.New(t)
	tr.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	tr.place = placement.New(t, placement.WithSentry(t, tr.sentry))
	tr.sched = scheduler.New(t, scheduler.WithSentry(tr.sentry), scheduler.WithID("dapr-scheduler-server-0"))

	tr.daprd = daprd.New(t,
		daprd.WithSentry(t, tr.sentry),
		daprd.WithPlacementAddresses(tr.place.Address()),
		daprd.WithScheduler(tr.sched),
		daprd.WithResourceFiles(tr.db.GetComponent(t)),
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
		framework.WithProcesses(tr.sentry, tr.db, tr.place, tr.sched, tr.daprd),
	}
}

func (tr *terminate) Run(t *testing.T, ctx context.Context) {
	tr.daprd.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("sign-term", func(ctx *dworkflow.WorkflowContext) (any, error) {
		if err := ctx.WaitForExternalEvent("never-coming", time.Second*60).Await(nil); err != nil {
			return nil, err
		}
		return nil, nil
	})

	client := dworkflow.NewClient(tr.daprd.GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "sign-term")
	require.NoError(t, err)

	meta, err := client.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, dworkflow.StatusRunning, meta.RuntimeStatus)

	require.NoError(t, client.TerminateWorkflow(ctx, id))

	meta, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, dworkflow.StatusTerminated, meta.RuntimeStatus)

	assert.Positive(t, tr.db.CountStateKeys(t, ctx, "signature"))
}
