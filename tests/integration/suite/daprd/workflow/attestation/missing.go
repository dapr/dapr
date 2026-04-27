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
	suite.Register(new(missing))
}

// missing models a heterogeneous deployment: the parent daprd has history
// signing enabled, but the child/activity daprd does NOT. When the child
// app delivers a completion event without an attestation, the parent must
// reject the event (not silently accept) — otherwise signing enablement
// on the parent would be cosmetic. The workflow should fail to make
// forward progress: the parent's inbox rejects each retry, and the task
// stays pending.
type missing struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	parent *daprd.Daprd
	child  *daprd.Daprd
	db     *sqlite.SQLite
}

func (m *missing) Setup(t *testing.T) []framework.Option {
	m.sentry = sentry.New(t)
	m.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	m.place = placement.New(t, placement.WithSentry(t, m.sentry))
	m.sched = scheduler.New(t, scheduler.WithSentry(m.sentry), scheduler.WithID("dapr-scheduler-server-0"))

	// Parent: signing ON — will require attestations on inbound events.
	m.parent = daprd.New(t,
		daprd.WithAppID("attest-parent-on"),
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

	// Child: signing OFF — will deliver completion events without
	// attestations.
	m.child = daprd.New(t,
		daprd.WithAppID("attest-child-off"),
		daprd.WithSentry(t, m.sentry),
		daprd.WithPlacementAddresses(m.place.Address()),
		daprd.WithScheduler(m.sched),
		daprd.WithResourceFiles(m.db.GetComponent(t)),
	)

	return []framework.Option{
		framework.WithProcesses(m.sentry, m.db, m.place, m.sched, m.parent, m.child),
	}
}

func (m *missing) Run(t *testing.T, ctx context.Context) {
	m.parent.WaitUntilRunning(t, ctx)
	m.child.WaitUntilRunning(t, ctx)

	regParent := dworkflow.NewRegistry()
	regParent.AddWorkflowN("attest-missing-parent", func(ctx *dworkflow.WorkflowContext) (any, error) {
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

	id, err := clientParent.ScheduleWorkflow(ctx, "attest-missing-parent")
	require.NoError(t, err)

	// Wait a generous window for the workflow to fail-to-progress. The
	// parent's ingestion keeps rejecting the unattested activity result,
	// so the workflow stays RUNNING and never reaches COMPLETED.
	time.Sleep(5 * time.Second)

	meta, err := clientParent.FetchWorkflowMetadata(ctx, id)
	require.NoError(t, err)
	assert.NotEqual(t, dworkflow.StatusCompleted, meta.RuntimeStatus,
		"workflow must not complete when the parent rejects unattested activity results")

	// No attestations were ever absorbed — ext-sigcert must remain empty.
	assert.Equal(t, 0, fworkflow.ExtSigCertCount(t, ctx, m.db, id))
}
