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

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/durabletask-go/backend"
)

// StateRows reads and writes one workflow instance's raw state rows, keyed by
// the suffix the runtime gives them ("metadata", "history-000003"). A test
// simulating a peer host's write needs the rows underneath the actor, and the
// two stores the suite uses reach them differently.
type StateRows interface {
	// Row returns the row's bytes, or nil when it does not exist.
	Row(t *testing.T, ctx context.Context, suffix string) []byte
	// SetRow overwrites the row.
	SetRow(t *testing.T, ctx context.Context, suffix string, raw []byte)
}

// SQLiteRows addresses instanceID's rows in a SQLite state store.
func SQLiteRows(db *sqlite.SQLite, instanceID string) StateRows {
	return sqliteRows{db: db, instanceID: instanceID}
}

type sqliteRows struct {
	db         *sqlite.SQLite
	instanceID string
}

func (s sqliteRows) Row(t *testing.T, ctx context.Context, suffix string) []byte {
	t.Helper()
	_, raw, _ := s.db.TryReadStateValue(t, ctx, s.instanceID, suffix)
	return raw
}

func (s sqliteRows) SetRow(t *testing.T, ctx context.Context, suffix string, raw []byte) {
	t.Helper()
	key, _, ok := s.db.TryReadStateValue(t, ctx, s.instanceID, suffix)
	require.True(t, ok, "no %s row to overwrite for instance %s", suffix, s.instanceID)
	s.db.WriteStateValue(t, ctx, key, raw)
}

// ComponentRows addresses the rows a state store component holds under the
// workflow actor's key prefix, which WorkflowActorKeyPrefix builds.
func ComponentRows(store state.Store, prefix string) StateRows {
	return componentRows{store: store, prefix: prefix}
}

type componentRows struct {
	store  state.Store
	prefix string
}

func (c componentRows) Row(t *testing.T, ctx context.Context, suffix string) []byte {
	t.Helper()
	res, err := c.store.Get(ctx, &state.GetRequest{Key: c.prefix + suffix})
	require.NoError(t, err)
	if res == nil {
		return nil
	}
	return res.Data
}

func (c componentRows) SetRow(t *testing.T, ctx context.Context, suffix string, raw []byte) {
	t.Helper()
	require.NoError(t, c.store.Set(ctx, &state.SetRequest{Key: c.prefix + suffix, Value: raw}))
}

// TaskScheduledRow returns the suffix and parsed event of the history row
// recording taskID's scheduling, or a nil event when no such row is readable
// yet.
func TaskScheduledRow(t *testing.T, ctx context.Context, rows StateRows, taskID int32) (string, *backend.HistoryEvent) {
	t.Helper()

	for i := 0; ; i++ {
		suffix := fmt.Sprintf("history-%06d", i)
		raw := rows.Row(t, ctx, suffix)
		if len(raw) == 0 {
			return "", nil
		}
		var ev backend.HistoryEvent
		require.NoError(t, proto.Unmarshal(raw, &ev))
		if ev.GetTaskScheduled() != nil && ev.GetEventId() == taskID {
			return suffix, &ev
		}
	}
}

// SetTaskExecutionID rewrites the durable scheduling of taskID so it names
// exec, and rewrites the metadata row so a cache holding the old view reads
// as stale. This is what a peer host advancing the instance looks like from
// underneath the actor.
func SetTaskExecutionID(t *testing.T, ctx context.Context, rows StateRows, taskID int32, exec string) {
	t.Helper()

	suffix, ev := TaskScheduledRow(t, ctx, rows, taskID)
	require.NotNil(t, ev, "task %d must be recorded in history", taskID)
	ev.GetTaskScheduled().TaskExecutionId = exec
	raw, err := proto.Marshal(ev)
	require.NoError(t, err)
	rows.SetRow(t, ctx, suffix, raw)

	// Rewritten unchanged on purpose: the actor detects staleness by ETag,
	// and any write moves it, so re-serialising the same metadata is enough
	// to make a cache holding the old view fail its next conditional save.
	var metadata backend.BackendWorkflowStateMetadata
	require.NoError(t, proto.Unmarshal(rows.Row(t, ctx, "metadata"), &metadata))
	updated, err := proto.Marshal(&metadata)
	require.NoError(t, err)
	rows.SetRow(t, ctx, "metadata", updated)
}
