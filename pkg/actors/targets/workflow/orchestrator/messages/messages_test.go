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

package messages

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

// Test_CallAddEventStateMessage_ChildCompletionNotFoundIsTerminal pins that a
// parent refusing a child completion with api.ErrInstanceNotFound (purged,
// tombstoned, or a dropped straggler after ContinueAsNew) is terminal: the
// dispatch is treated as delivered so the child's turn commits instead of
// redelivering forever, where a later redelivery can collide with a reused
// task id and be misread as tampering. Any other error keeps the dispatch
// failed so the turn retries.
func Test_CallAddEventStateMessage_ChildCompletionNotFoundIsTerminal(t *testing.T) {
	t.Parallel()

	newMsg := func() *backend.WorkflowRuntimeStateMessage {
		return &backend.WorkflowRuntimeStateMessage{
			TargetInstanceId: "parent-instance",
			HistoryEvent: &backend.HistoryEvent{
				EventId: 5,
				EventType: &protos.HistoryEvent_ChildWorkflowInstanceCompleted{
					ChildWorkflowInstanceCompleted: &protos.ChildWorkflowInstanceCompletedEvent{
						TaskScheduledId: 1,
					},
				},
			},
		}
	}

	newMessages := func(callErr error) (*Messages, *int) {
		calls := new(int)
		return &Messages{
			AppID:     "app",
			ActorID:   "child-instance",
			ActorType: "dapr.internal.default.app.workflow",
			Router: routerfake.New().WithCallFn(func(context.Context, *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
				*calls++
				return nil, callErr
			}),
		}, calls
	}

	t.Run("instance not found is treated as delivered", func(t *testing.T) {
		t.Parallel()
		m, calls := newMessages(errors.New("failed to invoke method 'AddWorkflowEvent' on actor 'parent-instance': no such instance exists"))
		res := m.CallAddEventStateMessage(t.Context(), []*backend.WorkflowRuntimeStateMessage{newMsg()}, nil)
		require.NoError(t, res.Err)
		assert.Empty(t, res.FailedEventIDs)
		assert.Equal(t, 1, *calls)
	})

	t.Run("permission denied is treated as delivered", func(t *testing.T) {
		t.Parallel()
		m, calls := newMessages(status.Error(codes.PermissionDenied, "workflow access policy denied"))
		res := m.CallAddEventStateMessage(t.Context(), []*backend.WorkflowRuntimeStateMessage{newMsg()}, nil)
		require.NoError(t, res.Err)
		assert.Empty(t, res.FailedEventIDs)
		assert.Equal(t, 1, *calls)
	})

	t.Run("other errors keep the dispatch failed", func(t *testing.T) {
		t.Parallel()
		m, calls := newMessages(errors.New("connection refused"))
		res := m.CallAddEventStateMessage(t.Context(), []*backend.WorkflowRuntimeStateMessage{newMsg()}, nil)
		require.Error(t, res.Err)
		assert.Len(t, res.FailedEventIDs, 1)
		assert.Equal(t, 1, *calls)
	})
}
