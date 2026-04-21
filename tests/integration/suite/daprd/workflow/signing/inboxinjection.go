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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(inboxInjection))
}

// inboxInjection verifies that injecting a fake TaskCompleted event into the
// inbox is rejected when signing is enabled. The workflow schedules a real
// activity and then waits for an external event. A TaskCompleted referencing
// a TaskScheduledId that was never scheduled in the signed history is purged
// by filterValidInboxEvents, leaving the workflow in RUNNING state without
// processing the injected event.
type inboxInjection struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	daprd  *daprd.Daprd
	db     *sqlite.SQLite
}

func (i *inboxInjection) Setup(tt *testing.T) []framework.Option {
	i.sentry = sentry.New(tt)
	i.db = sqlite.New(tt,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	i.place = placement.New(tt, placement.WithSentry(tt, i.sentry))
	i.sched = scheduler.New(tt, scheduler.WithSentry(i.sentry), scheduler.WithID("dapr-scheduler-server-0"))

	i.daprd = daprd.New(tt,
		daprd.WithSentry(tt, i.sentry),
		daprd.WithPlacementAddresses(i.place.Address()),
		daprd.WithScheduler(i.sched),
		daprd.WithResourceFiles(i.db.GetComponent(tt)),
		daprd.WithConfigManifests(tt, `apiVersion: dapr.io/v1alpha1
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
		framework.WithProcesses(i.sentry, i.db, i.place, i.sched, i.daprd),
	}
}

func (i *inboxInjection) Run(tt *testing.T, ctx context.Context) {
	i.daprd.WaitUntilRunning(tt, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("sign-inbox-inject", func(ctx *dworkflow.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("noop").Await(nil); err != nil {
			return nil, err
		}
		var payload string
		if err := ctx.WaitForExternalEvent("continue", time.Second*30).Await(&payload); err != nil {
			return nil, err
		}
		return payload, nil
	})
	reg.AddActivityN("noop", func(ctx dworkflow.ActivityContext) (any, error) {
		return nil, nil
	})

	client := dworkflow.NewClient(i.daprd.GRPCConn(tt, ctx))
	require.NoError(tt, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "sign-inbox-inject")
	require.NoError(tt, err)

	meta, err := client.WaitForWorkflowStart(ctx, id)
	require.NoError(tt, err)
	assert.Equal(tt, dworkflow.StatusRunning, meta.RuntimeStatus)

	// Wait until the activity has completed and been signed into history
	// (parent has scheduled and awaited the task, and is now waiting for
	// the external event).
	require.EventuallyWithT(tt, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, fworkflow.SignatureCount(tt, ctx, i.db, id), 2)
	}, time.Second*10, time.Millisecond*100)

	// Inject a fake TaskCompleted event into the inbox referencing a
	// TaskScheduledId (9999) that was never scheduled in the workflow history.
	appID := i.daprd.AppID()
	keyPrefix := appID + "||dapr.internal.default." + appID + ".workflow||" + id + "||"

	fakeEvt := &protos.HistoryEvent{
		EventId:   int32(-1),
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{
				TaskScheduledId: int32(9999),
				Result:          wrapperspb.String(`"injected"`),
			},
		},
	}
	raw, err := proto.Marshal(fakeEvt)
	require.NoError(tt, err)

	i.db.WriteStateValue(tt, ctx, fmt.Sprintf("%sinbox-%06d", keyPrefix, 0), raw)

	// Update the workflow metadata to reflect the injected inbox event.
	fworkflow.MutateMetadata(tt, ctx, i.db, id, func(m *backend.BackendWorkflowStateMetadata) {
		m.InboxLength = 1
	})

	// Restart daprd to clear the in-memory cache and force re-loading state
	// from the store.
	i.daprd.Restart(tt, ctx)
	i.daprd.WaitUntilRunning(tt, ctx)

	client = dworkflow.NewClient(i.daprd.GRPCConn(tt, ctx))
	require.NoError(tt, client.StartWorker(ctx, reg))

	// Inbox injection is treated as state store tampering: any operation that
	// loads the actor state detects the forged inbox entry and rejects the
	// call. RaiseEvent goes through the orchestrator actor, so it surfaces
	// the tampering error directly.
	err = client.RaiseEvent(ctx, id, "continue", dworkflow.WithEventPayload("real-event"))
	require.Error(tt, err)
	assert.Contains(tt, err.Error(), "state store tampering")
}
