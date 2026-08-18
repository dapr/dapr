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

package workflow

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/client"
)

// InjectInboxEvent appends evt to the workflow actor's persisted inbox in the
// SQLite state store and increments the metadata's InboxLength by one. The
// caller is responsible for invalidating the actor's in-memory cache (e.g. via
// daprd.Restart) before relying on the injection to take effect.
func InjectInboxEvent(t *testing.T, ctx context.Context, db *sqlite.SQLite, daprd *daprd.Daprd, instanceID string, evt *protos.HistoryEvent) {
	t.Helper()

	keyPrefix := workflowActorKeyPrefix(daprd, instanceID)

	raw, err := proto.Marshal(evt)
	require.NoError(t, err)

	idx := db.CountStateKeys(t, ctx, instanceID+"||inbox")
	db.WriteStateValue(t, ctx, fmt.Sprintf("%sinbox-%06d", keyPrefix, idx), raw)

	key, metaRaw := db.ReadStateValue(t, ctx, instanceID, "metadata")
	var metadata backend.BackendWorkflowStateMetadata
	require.NoError(t, proto.Unmarshal(metaRaw, &metadata))
	metadata.InboxLength++
	updated, err := proto.Marshal(&metadata)
	require.NoError(t, err)
	db.WriteStateValue(t, ctx, key, updated)
}

// RemoveHistoryEvent deletes the first history event matching pred and
// renumbers any subsequent history-* keys to keep the sequence contiguous.
// The metadata's HistoryLength is decremented by one.
func RemoveHistoryEvent(t *testing.T, ctx context.Context, db *sqlite.SQLite, daprd *daprd.Daprd, instanceID string, pred func(*protos.HistoryEvent) bool) {
	t.Helper()

	keyPrefix := workflowActorKeyPrefix(daprd, instanceID)

	values := db.ReadStateValues(t, ctx, instanceID, "history")
	target := -1
	for i, raw := range values {
		var ev protos.HistoryEvent
		require.NoError(t, proto.Unmarshal(raw, &ev))
		if pred(&ev) {
			target = i
			break
		}
	}
	require.GreaterOrEqual(t, target, 0, "no history event matched the predicate")

	conn := db.GetConnection(t)
	tableName := db.TableName()

	deleteKey := fmt.Sprintf("%shistory-%06d", keyPrefix, target)
	//nolint:gosec
	_, err := conn.ExecContext(ctx, "DELETE FROM "+tableName+" WHERE key = ?", deleteKey)
	require.NoError(t, err)

	for i := target + 1; i < len(values); i++ {
		oldKey := fmt.Sprintf("%shistory-%06d", keyPrefix, i)
		newKey := fmt.Sprintf("%shistory-%06d", keyPrefix, i-1)
		//nolint:gosec
		_, err = conn.ExecContext(ctx, "UPDATE "+tableName+" SET key = ? WHERE key = ?", newKey, oldKey)
		require.NoError(t, err)
	}

	key, metaRaw := db.ReadStateValue(t, ctx, instanceID, "metadata")
	var metadata backend.BackendWorkflowStateMetadata
	require.NoError(t, proto.Unmarshal(metaRaw, &metadata))
	require.Positive(t, metadata.HistoryLength)
	metadata.HistoryLength--
	updatedMeta, err := proto.Marshal(&metadata)
	require.NoError(t, err)
	db.WriteStateValue(t, ctx, key, updatedMeta)
}

// CountHistoryEventsMatching returns the number of events in the workflow's
// history (as exposed via GetInstanceHistory) that satisfy pred.
func CountHistoryEventsMatching(t *testing.T, ctx context.Context, cl *client.TaskHubGrpcClient, id api.InstanceID, pred func(*protos.HistoryEvent) bool) int {
	t.Helper()
	hist, err := cl.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	count := 0
	for _, e := range hist.GetEvents() {
		if pred(e) {
			count++
		}
	}
	return count
}

// IsTaskCompletedFor returns a predicate that matches a TaskCompleted event
// for the given TaskScheduledId.
func IsTaskCompletedFor(taskScheduledID int32) func(*protos.HistoryEvent) bool {
	return func(e *protos.HistoryEvent) bool {
		c := e.GetTaskCompleted()
		return c != nil && c.GetTaskScheduledId() == taskScheduledID
	}
}

func IsTaskFailedFor(taskScheduledID int32) func(*protos.HistoryEvent) bool {
	return func(e *protos.HistoryEvent) bool {
		c := e.GetTaskFailed()
		return c != nil && c.GetTaskScheduledId() == taskScheduledID
	}
}

func IsTimerFiredFor(timerID int32) func(*protos.HistoryEvent) bool {
	return func(e *protos.HistoryEvent) bool {
		c := e.GetTimerFired()
		return c != nil && c.GetTimerId() == timerID
	}
}

func IsChildCompletedFor(taskScheduledID int32) func(*protos.HistoryEvent) bool {
	return func(e *protos.HistoryEvent) bool {
		c := e.GetChildWorkflowInstanceCompleted()
		return c != nil && c.GetTaskScheduledId() == taskScheduledID
	}
}

func IsChildFailedFor(taskScheduledID int32) func(*protos.HistoryEvent) bool {
	return func(e *protos.HistoryEvent) bool {
		c := e.GetChildWorkflowInstanceFailed()
		return c != nil && c.GetTaskScheduledId() == taskScheduledID
	}
}

func IsTaskScheduledFor(eventID int32) func(*protos.HistoryEvent) bool {
	return func(e *protos.HistoryEvent) bool {
		return e.GetTaskScheduled() != nil && e.GetEventId() == eventID
	}
}

func workflowActorKeyPrefix(daprd *daprd.Daprd, instanceID string) string {
	appID := daprd.AppID()
	return appID + "||dapr.internal.default." + appID + ".workflow||" + instanceID + "||"
}
