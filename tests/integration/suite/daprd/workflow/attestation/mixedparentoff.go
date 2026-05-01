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
	suite.Register(new(mixedParentOffChildOn))
}

type mixedParentOffChildOn struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	parent *daprd.Daprd
	child  *daprd.Daprd
	db     *sqlite.SQLite
}

func (m *mixedParentOffChildOn) Setup(t *testing.T) []framework.Option {
	m.sentry = sentry.New(t)
	m.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	m.place = placement.New(t, placement.WithSentry(t, m.sentry))
	m.sched = scheduler.New(t, scheduler.WithSentry(m.sentry), scheduler.WithID("dapr-scheduler-server-0"))

	m.parent = daprd.New(t,
		daprd.WithAppID("attest-parent-off"),
		daprd.WithSentry(t, m.sentry),
		daprd.WithPlacementAddresses(m.place.Address()),
		daprd.WithScheduler(m.sched),
		daprd.WithResourceFiles(m.db.GetComponent(t)),
	)

	m.child = daprd.New(t,
		daprd.WithAppID("attest-child-on"),
		daprd.WithSentry(t, m.sentry),
		daprd.WithPlacementAddresses(m.place.Address()),
		daprd.WithScheduler(m.sched),
		daprd.WithResourceFiles(m.db.GetComponent(t)),
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
		framework.WithProcesses(m.sentry, m.db, m.place, m.sched, m.parent, m.child),
	}
}

func (m *mixedParentOffChildOn) Run(t *testing.T, ctx context.Context) {
	m.parent.WaitUntilRunning(t, ctx)
	m.child.WaitUntilRunning(t, ctx)

	regParent := dworkflow.NewRegistry()
	regParent.AddWorkflowN("attest-mixed-parent-off", func(ctx *dworkflow.WorkflowContext) (any, error) {
		return nil, ctx.CallActivity("remote-noop",
			dworkflow.WithActivityAppID(m.child.AppID()),
		).Await(nil)
	})

	regChild := dworkflow.NewRegistry()
	regChild.AddActivityN("remote-noop", func(ctx dworkflow.ActivityContext) (any, error) {
		return nil, nil
	})

	clientParent := dworkflow.NewClient(m.parent.GRPCConn(t, ctx))
	require.NoError(t, clientParent.StartWorker(ctx, regParent))

	clientChild := dworkflow.NewClient(m.child.GRPCConn(t, ctx))
	require.NoError(t, clientChild.StartWorker(ctx, regChild))

	id, err := clientParent.ScheduleWorkflow(ctx, "attest-mixed-parent-off")
	require.NoError(t, err)

	meta, err := clientParent.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, dworkflow.StatusCompleted, meta.RuntimeStatus,
		"workflow must complete when the parent has signing off, even if the child sends an attestation")

	assert.Equal(t, 0, fworkflow.ExtSigCertCount(t, ctx, m.db, id))
}
