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
	"google.golang.org/protobuf/proto"

	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/state/errors"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(tamperedrunning))
}

// tamperedrunning verifies that a running (non-terminal) workflow with a
// tampered history event is, on the next orchestrator-actor activation,
// terminally marked FAILED with the well-known
// [wferrors.ErrorTypeHistoryTampered] error type — so the executor stops
// working on the workflow.
type tamperedrunning struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	daprd  *daprd.Daprd
	db     *sqlite.SQLite
}

func (t *tamperedrunning) Setup(tt *testing.T) []framework.Option {
	t.sentry = sentry.New(tt)
	t.db = sqlite.New(tt,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	t.place = placement.New(tt, placement.WithSentry(tt, t.sentry))
	t.sched = scheduler.New(tt, scheduler.WithSentry(t.sentry), scheduler.WithID("dapr-scheduler-server-0"))

	t.daprd = daprd.New(tt,
		daprd.WithSentry(tt, t.sentry),
		daprd.WithPlacementAddresses(t.place.Address()),
		daprd.WithScheduler(t.sched),
		daprd.WithResourceFiles(t.db.GetComponent(tt)),
		daprd.WithConfigManifests(tt, `apiVersion: dapr.io/v1alpha1
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
		framework.WithProcesses(t.sentry, t.db, t.place, t.sched, t.daprd),
	}
}

func (tr *tamperedrunning) Run(tt *testing.T, ctx context.Context) {
	tr.daprd.WaitUntilRunning(tt, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("sign-tamper-running", func(ctx *dworkflow.WorkflowContext) (any, error) {
		var payload string
		if err := ctx.WaitForExternalEvent("continue", time.Second*30).Await(&payload); err != nil {
			return nil, err
		}
		return payload, nil
	})

	client := dworkflow.NewClient(tr.daprd.GRPCConn(tt, ctx))
	require.NoError(tt, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "sign-tamper-running")
	require.NoError(tt, err)

	meta, err := client.WaitForWorkflowStart(ctx, id)
	require.NoError(tt, err)
	assert.Equal(tt, dworkflow.StatusRunning, meta.RuntimeStatus)

	// Tamper with a history event by modifying its EventId.
	histKey, raw := tr.db.FirstStateValue(tt, ctx, id, "history")

	var evt protos.HistoryEvent
	require.NoError(tt, proto.Unmarshal(raw, &evt))

	evt.EventId += 9999

	updated, err := proto.Marshal(&evt)
	require.NoError(tt, err)

	tr.db.WriteStateValue(tt, ctx, histKey, updated)

	// Restart daprd to clear any cached state.
	tr.daprd.Restart(tt, ctx)
	tr.daprd.WaitUntilRunning(tt, ctx)

	client = dworkflow.NewClient(tr.daprd.GRPCConn(tt, ctx))
	require.NoError(tt, client.StartWorker(ctx, reg))

	// Trigger the orchestrator actor by raising the event the workflow is
	// waiting on. The actor's load detects the tampered history and appends
	// the terminal tamper marker so the workflow stops being worked on.
	require.NoError(tt, client.RaiseEvent(ctx, id, "continue", dworkflow.WithEventPayload("real-event")))

	require.EventuallyWithT(tt, func(c *assert.CollectT) {
		meta, err := client.FetchWorkflowMetadata(ctx, id)
		assert.NoError(c, err)
		if !assert.Equal(c, dworkflow.StatusFailed, meta.RuntimeStatus) {
			return
		}
		if !assert.NotNil(c, meta.FailureDetails) {
			return
		}
		assert.Equal(c, wferrors.ErrorTypeHistoryTampered, meta.FailureDetails.GetErrorType())
		assert.Contains(c, meta.FailureDetails.GetErrorMessage(), "signature verification failed")
	}, time.Second*10, time.Millisecond*10)
}
