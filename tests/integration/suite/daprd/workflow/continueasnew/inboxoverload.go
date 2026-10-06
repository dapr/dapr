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
	"strings"
	"sync"
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
)

func init() {
	suite.Register(new(inboxoverload))
}

// inboxoverload injects 25 events with unique payloads directly into the state
// store, guaranteeing MaxContinueAsNewCount (20) is exceeded. The workflow
// tracks every received payload. This verifies each payload is received
// exactly once, none are lost, and ordering is preserved.
type inboxoverload struct {
	workflow *workflow.Workflow
}

func (i *inboxoverload) Setup(t *testing.T) []framework.Option {
	// Signing mode opt-out: the test hand-writes unsigned inbox rows into sqlite (rejected by a signing-enabled host) and dials the scheduler with a plaintext client.
	i.workflow = workflow.New(t, workflow.WithSigning(false))
	return []framework.Option{
		framework.WithProcesses(i.workflow),
	}
}

func (i *inboxoverload) Run(t *testing.T, ctx context.Context) {
	i.workflow.WaitUntilRunning(t, ctx)

	const totalEvents = 25

	var mu sync.Mutex
	var payloads []string
	var drainMode atomic.Bool

	i.workflow.Registry().AddWorkflowN("inboxoverload", func(ctx *task.WorkflowContext) (any, error) {
		var inc int
		require.NoError(t, ctx.GetInput(&inc))

		var val string
		ctx.WaitForSingleEvent("ev", 15*time.Second).Await(&val)
		if val == "" {
			if drainMode.Load() {
				return inc, nil
			}
			ctx.ContinueAsNew(inc, task.WithKeepUnprocessedEvents())
			return nil, nil
		}

		mu.Lock()
		payloads = append(payloads, val)
		mu.Unlock()

		ctx.ContinueAsNew(inc+1, task.WithKeepUnprocessedEvents())
		return nil, nil
	})

	client := i.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "inboxoverload",
		api.WithInstanceID("inboxoverloadi"),
		api.WithInput(0),
	)
	require.NoError(t, err)

	_, err = client.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	appID := i.workflow.Dapr().AppID()
	actorType := "dapr.internal.default." + appID + ".workflow"
	actorID := "inboxoverloadi"

	_, err = i.workflow.Scheduler().Client(t, ctx).ScheduleJob(ctx,
		i.workflow.Scheduler().JobNowActor("new-event-deactivate", "default", appID, actorType, actorID))
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var count int
		for _, key := range i.workflow.Scheduler().ListAllKeys(t, ctx, "dapr/jobs") {
			if strings.Contains(key, "new-event-deactivate") {
				count++
			}
		}
		assert.Zero(c, count, "the injected empty-inbox reminder must be acked and deleted")
	}, 10*time.Second, 50*time.Millisecond)

	// The new residency contract: the live workflow remains active after
	// the empty-inbox ack.
	dmeta := i.workflow.Dapr().GetMetadata(t, ctx)
	require.NotNil(t, dmeta.ActorRuntime)
	found := false
	for _, a := range dmeta.ActorRuntime.ActiveActors {
		if a.Type == actorType {
			found = true
			require.Equal(t, 1, a.Count, "live workflow actor %q must stay resident after an empty-inbox ack", actorType)
		}
	}
	require.True(t, found, "workflow actor type %q must be active; absence means it was deactivated", actorType)

	db := i.workflow.DB().GetConnection(t)
	tableName := i.workflow.DB().TableName()
	writeUniquePayloadInbox(t, ctx, db, tableName, appID, actorType, actorID, totalEvents)

	_, err = i.workflow.Scheduler().Client(t, ctx).ScheduleJob(ctx,
		i.workflow.Scheduler().JobNowActor("new-event-batch", "default", appID, actorType, actorID))
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		mu.Lock()
		n := len(payloads)
		mu.Unlock()
		assert.Equal(c, totalEvents, n, "waiting for all events to be processed before draining")
	}, time.Second*30, 10*time.Millisecond)
	drainMode.Store(true)

	wmeta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, wmeta.GetOutput())

	mu.Lock()
	defer mu.Unlock()

	assert.Len(t, payloads, totalEvents)

	seen := make(map[string]int)
	for _, p := range payloads {
		seen[p]++
	}
	for p, count := range seen {
		assert.Equal(t, 1, count, "payload %q seen %d times", p, count)
	}

	for idx, p := range payloads {
		assert.Equal(t, fmt.Sprintf("payload-%d", idx), p,
			"event at position %d has wrong payload", idx)
	}
}

func writeUniquePayloadInbox(t *testing.T, ctx context.Context, db *sql.DB, tableName, appID, actorType, actorID string, n int) {
	t.Helper()

	keyPrefix := appID + "||" + actorType + "||" + actorID + "||"

	for j := range n {
		evt := &protos.HistoryEvent{
			EventId:   int32(j),
			Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_EventRaised{
				EventRaised: &protos.EventRaisedEvent{
					Name:  "ev",
					Input: wrapperspb.String(fmt.Sprintf(`"payload-%d"`, j)),
				},
			},
		}
		raw, err := proto.Marshal(evt)
		require.NoError(t, err)

		encoded := base64.StdEncoding.EncodeToString(raw)
		key := fmt.Sprintf("%sinbox-%06d", keyPrefix, j)
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
