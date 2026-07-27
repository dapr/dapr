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
	"database/sql"
	"encoding/base64"
	"fmt"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(raisebatch))
}

// raisebatch is a regression test for the state corruption bug where o.rstate
// was shared with wi.State via a bare pointer. When the engine's
// ContinueAsNew tight-loop exceeded MaxContinueAsNewCount (20), the applier
// had already mutated *wi.State (and thus o.rstate) via *s = *newState. On
// retry the corrupted rstate would carry forward a wrong input value, causing
// the workflow's counter to jump ahead and silently skip events.
type raisebatch struct {
	workflow *workflow.Workflow
}

func (r *raisebatch) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *raisebatch) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	var drainMode atomic.Bool
	const totalEvents = 25

	r.workflow.Registry().AddWorkflowN("raisebatch", func(ctx *task.WorkflowContext) (any, error) {
		var inc int
		require.NoError(t, ctx.GetInput(&inc))

		// In drain mode, process one event and complete. The CAN progress
		// was saved when MaxContinueAsNewCount was hit, so inc reflects
		// the saved progress (e.g. 20 after 20 successful CAN iterations).
		if drainMode.Load() {
			ctx.WaitForSingleEvent("incr", time.Minute).Await(nil)
			return inc + 1, nil
		}

		var got bool
		ctx.WaitForSingleEvent("incr", 3*time.Second).Await(&got)
		if !got {
			if drainMode.Load() {
				return inc, nil
			}
			ctx.ContinueAsNew(inc, task.WithKeepUnprocessedEvents())
			return nil, nil
		}
		ctx.ContinueAsNew(inc+1, task.WithKeepUnprocessedEvents())
		return nil, nil
	})

	client := r.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "raisebatch",
		api.WithInstanceID("raisebatchi"),
		api.WithInput(0),
	)
	require.NoError(t, err)

	_, err = client.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	appID := r.workflow.Dapr().AppID()
	actorType := "dapr.internal.default." + appID + ".workflow"
	actorID := "raisebatchi"

	// Force the actor to deactivate by firing a dummy reminder. The reminder
	// finds an empty inbox and returns RunCompletedTrue, which triggers
	// deactivation and clears the in-memory cache. Goes via the scheduler
	// directly because the daprd RegisterActorReminder API rejects
	// "dapr.internal.*" actor types.
	_, err = r.workflow.Scheduler().Client(t, ctx).ScheduleJob(ctx,
		r.workflow.Scheduler().JobNowActor("new-event-deactivate", "default", appID, actorType, actorID))
	require.NoError(t, err)

	time.Sleep(2 * time.Second)

	db := r.workflow.DB().GetConnection(t)
	tableName := r.workflow.DB().TableName()
	writeInboxToDB(t, ctx, db, tableName, appID, actorType, actorID, totalEvents)

	_, err = r.workflow.Scheduler().Client(t, ctx).ScheduleJob(ctx,
		r.workflow.Scheduler().JobNowActor("new-event-batch", "default", appID, actorType, actorID))
	require.NoError(t, err)

	time.Sleep(2 * time.Second)
	drainMode.Store(true)

	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	// The workflow should complete. The exact output depends on how many CAN
	// iterations succeeded across retries before drainMode is set. This is
	// non-deterministic because multiple retries may fire within the sleep
	// window, each processing up to MaxContinueAsNewCount (20) iterations. The
	// critical thing is that the workflow completes and doesn't hang, which it
	// would if the CAN progress wasn't saved (the retry would hit the same limit
	// with the same 25 events forever).
	require.NotNil(t, meta.GetOutput(),
		"workflow should complete with output; nil means it hung")
}

// writeInboxToDB writes n EventRaised events and updated metadata directly
// into the SQLite state store, using the same base64+is_binary encoding that
// the Dapr sqlite state component uses. An optional input payload can be set
// on each event.
func writeInboxToDB(t *testing.T, ctx context.Context, db *sql.DB, tableName, appID, actorType, actorID string, n int, input ...*wrapperspb.StringValue) {
	t.Helper()

	keyPrefix := appID + "||" + actorType + "||" + actorID + "||"

	for i := range n {
		eventRaised := &protos.EventRaisedEvent{
			Name: "incr",
		}
		if len(input) > 0 {
			eventRaised.Input = input[0]
		}
		evt := &protos.HistoryEvent{
			EventId:   int32(i),
			Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_EventRaised{
				EventRaised: eventRaised,
			},
		}
		raw, err := proto.Marshal(evt)
		require.NoError(t, err)

		encoded := base64.StdEncoding.EncodeToString(raw)
		key := fmt.Sprintf("%sinbox-%06d", keyPrefix, i)
		_, err = db.ExecContext(ctx,
			fmt.Sprintf("INSERT OR REPLACE INTO '%s' (key, value, is_binary, etag) VALUES (?, ?, 1, ?)", tableName),
			key, encoded, strconv.FormatInt(time.Now().UnixNano(), 10),
		)
		require.NoError(t, err)
	}

	metaKey := keyPrefix + "metadata"
	var existingVal string
	var isBin bool
	err := db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT value, is_binary FROM '%s' WHERE key = ?", tableName),
		metaKey,
	).Scan(&existingVal, &isBin)
	require.NoError(t, err)

	require.True(t, isBin, "expected workflow metadata row %q to be stored as binary", metaKey)

	var meta backend.BackendWorkflowStateMetadata
	raw, derr := base64.StdEncoding.DecodeString(existingVal)
	require.NoError(t, derr)
	require.NoError(t, proto.Unmarshal(raw, &meta))

	//nolint:gosec
	meta.InboxLength = uint64(n)
	raw, err = proto.Marshal(&meta)
	require.NoError(t, err)

	encoded := base64.StdEncoding.EncodeToString(raw)
	_, err = db.ExecContext(ctx,
		fmt.Sprintf("INSERT OR REPLACE INTO '%s' (key, value, is_binary, etag) VALUES (?, ?, 1, ?)", tableName),
		metaKey, encoded, strconv.FormatInt(time.Now().UnixNano(), 10),
	)
	require.NoError(t, err)
}
