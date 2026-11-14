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

package state

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/kit/ptr"
)

func TestOrdering(t *testing.T) {
	started := func(key *protos.WorkflowOrdering) *backend.HistoryEvent {
		return &backend.HistoryEvent{EventType: &protos.HistoryEvent_ExecutionStarted{ExecutionStarted: &protos.ExecutionStartedEvent{
			WorkflowInstance: &protos.WorkflowInstance{InstanceId: "i", ExecutionId: wrapperspb.String("e1")},
			Ordering:         key,
		}}}
	}
	completed := &backend.HistoryEvent{EventType: &protos.HistoryEvent_ExecutionCompleted{ExecutionCompleted: &protos.ExecutionCompletedEvent{}}}

	t.Run("a keyed instance is gated until released, and owes Done once completed", func(t *testing.T) {
		s := NewState(Options{})
		start := started(&protos.WorkflowOrdering{Key: "k"})
		s.StartOrdering(start.GetExecutionStarted())
		s.AddToInbox(start)
		assert.Equal(t, "e1", s.Ordering.GetCreateExecutionId())
		assert.Equal(t, "k", s.OrderingOptions().GetKey())
		assert.True(t, s.OrderingGated())

		s.Ordering.Released = ptr.Of(true)
		assert.False(t, s.OrderingGated())
		assert.False(t, s.OrderingDoneOwed())

		s.ClearInbox()
		s.AddToHistory(start)
		s.AddToHistory(completed)
		assert.True(t, s.OrderingDoneOwed())
		s.Ordering.Done = ptr.Of(true)
		assert.False(t, s.OrderingDoneOwed())

		md := savedMetadata(t, s)
		assert.True(t, proto.Equal(s.Ordering, md.GetOrdering()), "the metadata row carries the ordering state")

		s.Reset()
		assert.Nil(t, s.Ordering, "a recreated instance starts unordered until its own create")
		assert.Nil(t, savedMetadata(t, s).Ordering)
	})
}

func savedMetadata(t *testing.T, s *State) *backend.BackendWorkflowStateMetadata {
	t.Helper()
	req, err := s.GetSaveRequest("i")
	require.NoError(t, err)
	for _, op := range req.Operations {
		if u, ok := op.Request.(api.TransactionalUpsert); ok && u.Key == MetadataKey {
			md := new(backend.BackendWorkflowStateMetadata)
			require.NoError(t, proto.Unmarshal(u.Value.([]byte), md))
			return md
		}
	}
	require.Fail(t, "no metadata upsert")
	return nil
}
