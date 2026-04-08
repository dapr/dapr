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

	"github.com/stretchr/testify/assert"
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

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
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

		if drainMode.Load() {
			var got bool
			ctx.WaitForSingleEvent("incr", 3*time.Second).Await(&got)
			if !got {
				return inc, nil
			}
			eventCount.Add(1)
			inc++
			ctx.ContinueAsNew(inc, task.WithKeepUnprocessedEvents())
			return nil, nil
		}

		ctx.WaitForSingleEvent("incr", time.Minute).Await(nil)
		eventCount.Add(1)
		ctx.ContinueAsNew(inc+1, task.WithKeepUnprocessedEvents())
		return nil, nil
	})

	client := r.workflow.BackendClient(t, ctx)
	gclient := r.workflow.GRPCClient(t, ctx)

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

	_, err = gclient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: actorType,
		ActorId:   actorID,
		Name:      "new-event-deactivate",
		DueTime:   "0s",
	})
	require.NoError(t, err)

	time.Sleep(2 * time.Second)

	db := r.workflow.DB().GetConnection(t)
	tableName := r.workflow.DB().TableName()
	writeInboxWithPayloadToDB(t, ctx, db, tableName, appID, actorType, actorID, totalEvents)

	_, err = gclient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: actorType,
		ActorId:   actorID,
		Name:      "new-event-batch",
		DueTime:   "0s",
	})
	require.NoError(t, err)

	time.Sleep(3 * time.Second)
	drainMode.Store(true)

	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, meta.GetOutput(), "workflow should complete with output")

	assert.Equal(t, int64(totalEvents), eventCount.Load(),
		"each event should be processed exactly once; duplicates indicate "+
			"the inbox was not properly replaced with carryover events "+
			"during CAN progress save")
}

// writeInboxWithPayloadToDB writes n EventRaised events with a `true` payload
// directly into the SQLite state store.
func writeInboxWithPayloadToDB(t *testing.T, ctx context.Context, db *sql.DB, tableName, appID, actorType, actorID string, n int) {
	t.Helper()

	keyPrefix := appID + "||" + actorType + "||" + actorID + "||"

	for i := range n {
		evt := &protos.HistoryEvent{
			EventId:   int32(i),
			Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_EventRaised{
				EventRaised: &protos.EventRaisedEvent{
					Name:  "incr",
					Input: wrapperspb.String(`true`),
				},
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
	require.True(t, isBin)

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
