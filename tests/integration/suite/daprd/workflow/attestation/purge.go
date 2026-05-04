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

package attestation

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
	suite.Register(new(purge))
}

type purge struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	daprd  *daprd.Daprd
	db     *sqlite.SQLite
}

func (p *purge) Setup(t *testing.T) []framework.Option {
	p.sentry = sentry.New(t)
	p.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	p.place = placement.New(t, placement.WithSentry(t, p.sentry))
	p.sched = scheduler.New(t, scheduler.WithSentry(p.sentry), scheduler.WithID("dapr-scheduler-server-0"))

	p.daprd = daprd.New(t,
		daprd.WithSentry(t, p.sentry),
		daprd.WithPlacementAddresses(p.place.Address()),
		daprd.WithScheduler(p.sched),
		daprd.WithResourceFiles(p.db.GetComponent(t)),
		daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: attest-on
spec:
  features:
  - name: WorkflowHistorySigning
    enabled: true
`),
	)

	return []framework.Option{
		framework.WithProcesses(p.sentry, p.db, p.place, p.sched, p.daprd),
	}
}

func (p *purge) Run(t *testing.T, ctx context.Context) {
	p.daprd.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("attest-purge", func(ctx *dworkflow.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("noop").Await(nil); err != nil {
			return nil, err
		}
		return "", nil
	})
	reg.AddActivityN("noop", func(ctx dworkflow.ActivityContext) (any, error) {
		return nil, nil
	})

	client := dworkflow.NewClient(p.daprd.GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "attest-purge")
	require.NoError(t, err)

	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	require.GreaterOrEqual(t, fworkflow.ExtSigCertCount(t, ctx, p.db, id), 1)

	require.NoError(t, client.PurgeWorkflowState(ctx, id))

	assert.Equal(t, 0, fworkflow.ExtSigCertCount(t, ctx, p.db, id))
}
