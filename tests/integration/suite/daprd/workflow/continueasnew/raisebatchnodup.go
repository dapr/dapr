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

package continueasnew

import (
	"context"
	"encoding/base64"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(raisebatchnodup))
}

// raisebatchnodup verifies that when the ContinueAsNew tight-loop exceeds
// MaxContinueAsNewCount and partial CAN progress is saved, events are NOT
// duplicated on retry.
type raisebatchnodup struct {
	workflow *workflow.Workflow
}

func (r *raisebatchnodup) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *raisebatchnodup) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	var eventCount atomic.Int64
	var drainMode atomic.Bool
	const totalEvents = 25

	r.workflow.Registry().AddWorkflowN("raisebatchnodup", func(ctx *task.WorkflowContext) (any, error) {
		var inc int
		require.NoError(t, ctx.GetInput(&inc))

		var got bool
		ctx.WaitForSingleEvent("incr", 3*time.Second).Await(&got)
		if !got {
			if drainMode.Load() {
				return inc, nil
			}
			ctx.ContinueAsNew(inc, task.WithKeepUnprocessedEvents())
			return nil, nil
		}
		eventCount.Add(1)
		ctx.ContinueAsNew(inc+1, task.WithKeepUnprocessedEvents())
		return nil, nil
	})

	client := r.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "raisebatchnodup",
		api.WithInstanceID("raisebatchnodupi"),
		api.WithInput(0),
	)
	require.NoError(t, err)

	_, err = client.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	appID := r.workflow.Dapr().AppID()
	actorType := "dapr.internal.default." + appID + ".workflow"
	actorID := "raisebatchnodupi"

	// Inject the deactivation reminder via the scheduler directly: the
	// daprd RegisterActorReminder API rejects "dapr.internal.*" actor
	// types because they are reserved for the workflow runtime.
	_, err = r.workflow.Scheduler().Client(t, ctx).ScheduleJob(ctx,
		r.workflow.Scheduler().JobNowActor("new-event-deactivate", "default", appID, actorType, actorID))
	require.NoError(t, err)

	// Wait for the deactivation reminder to be processed. Poll the state
	// store until the workflow metadata generation has advanced (CAN
	// increments generation).
	db := r.workflow.DB().GetConnection(t)
	tableName := r.workflow.DB().TableName()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var gen int64
		metaKey := appID + "||" + actorType + "||" + actorID + "||metadata"
		//nolint:gosec
		row := db.QueryRowContext(ctx, "SELECT value FROM '"+tableName+"' WHERE key = ?", metaKey)
		var val string
		if !assert.NoError(c, row.Scan(&val)) {
			return
		}
		raw, derr := base64.StdEncoding.DecodeString(val)
		if !assert.NoError(c, derr) {
			return
		}
		var meta backend.BackendWorkflowStateMetadata
		if !assert.NoError(c, proto.Unmarshal(raw, &meta)) {
			return
		}
		//nolint:gosec
		gen = int64(meta.GetGeneration())
		assert.Greater(c, gen, int64(1), "generation should advance from CAN")
	}, 10*time.Second, 10*time.Millisecond)

	writeInboxToDB(t, ctx, db, tableName, appID, actorType, actorID, totalEvents, wrapperspb.String(`true`))

	_, err = r.workflow.Scheduler().Client(t, ctx).ScheduleJob(ctx,
		r.workflow.Scheduler().JobNowActor("new-event-batch", "default", appID, actorType, actorID))
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, eventCount.Load(), int64(totalEvents),
			"waiting for all events to be processed")
	}, 30*time.Second, 10*time.Millisecond)

	time.Sleep(2 * time.Second)
	drainMode.Store(true)

	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, meta.GetOutput(), "workflow should complete with output")

	assert.Equal(t, int64(totalEvents), eventCount.Load(),
		"each event should be processed exactly once")
}
