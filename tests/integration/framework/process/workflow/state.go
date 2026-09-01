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

	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

// WorkflowActorType returns the orchestrator actor type registered by the
// daprd at the given index (default namespace).
func (w *Workflow) WorkflowActorType(index int) string {
	return "dapr.internal.default." + w.daprds[index].AppID() + ".workflow"
}

// WriteWorkflowState writes a fabricated durable workflow state for the given
// instance straight into the SQLite actor state store: the history and inbox
// event rows plus the metadata row describing them, in the exact key layout
// the workflow state loader reads. Existing history and inbox rows for the
// instance are deleted first so the metadata lengths stay authoritative.
// daprd in-memory caches are not touched; pair this with a scheduler-driven
// reminder or a fresh actor activation to make daprd observe the rows.
func (w *Workflow) WriteWorkflowState(t *testing.T, ctx context.Context, index int, instanceID string, generation uint64, history, inbox []*protos.HistoryEvent) {
	t.Helper()

	keyPrefix := w.daprds[index].AppID() + "||" + w.WorkflowActorType(index) + "||" + instanceID + "||"

	w.db.DeleteStateKeys(t, ctx, keyPrefix+"history-%")
	w.db.DeleteStateKeys(t, ctx, keyPrefix+"inbox-%")

	for i, e := range history {
		raw, err := proto.Marshal(e)
		require.NoError(t, err)
		w.db.WriteStateValue(t, ctx, fmt.Sprintf("%shistory-%06d", keyPrefix, i), raw)
	}
	for i, e := range inbox {
		raw, err := proto.Marshal(e)
		require.NoError(t, err)
		w.db.WriteStateValue(t, ctx, fmt.Sprintf("%sinbox-%06d", keyPrefix, i), raw)
	}

	meta := &backend.BackendWorkflowStateMetadata{
		Generation:    generation,
		HistoryLength: uint64(len(history)),
		InboxLength:   uint64(len(inbox)),
	}
	raw, err := proto.Marshal(meta)
	require.NoError(t, err)
	w.db.WriteStateValue(t, ctx, keyPrefix+"metadata", raw)
}
