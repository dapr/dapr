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
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/dapr/dapr/pkg/actors/api"
	statefake "github.com/dapr/dapr/pkg/actors/state/fake"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/kit/ptr"
)

// metadataRow returns the raw metadata row that req writes.
func metadataRow(t *testing.T, req *api.TransactionalRequest) []byte {
	t.Helper()
	for _, op := range req.Operations {
		if u, ok := op.Request.(api.TransactionalUpsert); ok && u.Key == MetadataKey {
			b, ok := u.Value.([]byte)
			require.True(t, ok)
			return b
		}
	}
	require.Fail(t, "no metadata upsert in the save request")
	return nil
}

// savedSummary returns the summary in the metadata row that req writes.
func savedSummary(t *testing.T, req *api.TransactionalRequest) *backend.BackendWorkflowSummary {
	t.Helper()
	var meta backend.BackendWorkflowStateMetadata
	require.NoError(t, proto.Unmarshal(metadataRow(t, req), &meta))
	require.NotNil(t, meta.GetSummary(), "metadata row has no summary")
	return meta.GetSummary()
}

// save builds the save request, as a successful save does, and returns the
// summary that it wrote.
func save(t *testing.T, s *State) *backend.BackendWorkflowSummary {
	t.Helper()
	req, err := s.GetSaveRequest("wf-1")
	require.NoError(t, err)
	sum := savedSummary(t, req)
	s.ResetChangeTracking()
	return sum
}

func requireSummary(t *testing.T, want, got *backend.BackendWorkflowSummary) {
	t.Helper()
	require.Truef(t, proto.Equal(want, got), "want %v\ngot  %v", want, got)
}

func TestGetSaveRequest_Summary(t *testing.T) {
	t.Parallel()

	t.Run("every save writes the summary of the saved state", func(t *testing.T) {
		t.Parallel()
		s := NewState(testOpts())
		s.AddToInbox(sevExecStarted(0, "wf", nil))
		sum := save(t, s)
		assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_PENDING, sum.GetRuntimeStatus())
		assert.Equal(t, sevAt(0), sum.GetCreatedAt().AsTime())
		requireSummary(t, sum, s.Summary())

		// The first turn moves the start event into the history.
		s.ClearInbox()
		s.AddToHistory(sevStarted(1, "v1"))
		s.AddToHistory(sevExecStarted(0, "wf", nil))
		s.AddToHistory(sevTask(2, 0))
		sum = save(t, s)
		requireSummary(t, Summarize(s.History, nil), sum)
		assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_RUNNING, sum.GetRuntimeStatus())

		// An inbox-only save keeps the summary.
		s.AddToInbox(sevRaised(3))
		sum = save(t, s)
		requireSummary(t, Summarize(s.History, s.Inbox), sum)

		s.ClearInbox()
		s.AddToHistory(sevRaised(3))
		s.AddToHistory(sevCompleted(4, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED))
		sum = save(t, s)
		requireSummary(t, Summarize(s.History, nil), sum)
		assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, sum.GetRuntimeStatus())
		assert.Equal(t, sevAt(4), sum.GetCompletedAt().AsTime())
	})

	t.Run("GetSaveRequest twice gives the same row and keeps the committed summary", func(t *testing.T) {
		t.Parallel()
		s := NewState(testOpts())
		s.AddToHistory(sevStarted(1, ""))
		s.AddToHistory(sevExecStarted(0, "wf", nil))
		committed := save(t, s)

		s.AddToHistory(sevSuspended(2))
		req1, err := s.GetSaveRequest("wf-1")
		require.NoError(t, err)
		requireSummary(t, committed, s.Summary())
		req2, err := s.GetSaveRequest("wf-1")
		require.NoError(t, err)
		requireSummary(t, committed, s.Summary())
		assert.Equal(t, metadataRow(t, req1), metadataRow(t, req2))

		s.ResetChangeTracking()
		assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_SUSPENDED, s.Summary().GetRuntimeStatus())
	})

	t.Run("a failed save leaves the committed summary", func(t *testing.T) {
		t.Parallel()
		s := NewState(testOpts())
		s.AddToHistory(sevStarted(1, ""))
		s.AddToHistory(sevExecStarted(0, "wf", nil))
		committed := save(t, s)

		s.AddToHistory(sevCompleted(2, protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED))
		_, err := s.GetSaveRequest("wf-1")
		require.NoError(t, err)
		// The save fails: ResetChangeTracking is not called.
		requireSummary(t, committed, s.Summary())

		// The retry writes the same summary.
		sum := save(t, s)
		assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED, sum.GetRuntimeStatus())
	})

	t.Run("the save reads only the new events", func(t *testing.T) {
		t.Parallel()
		// The committed summary says RUNNING, but the unchanged history
		// holds a completion. A save that read the whole history would
		// report COMPLETED.
		s := NewState(testOpts())
		s.History = []*backend.HistoryEvent{
			sevStarted(1, ""), sevExecStarted(0, "wf", nil),
			sevCompleted(2, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED),
		}
		s.summary, s.summaryFacts = summarize(s.History[:2], nil)

		s.AddToHistory(sevRaised(3))
		sum := save(t, s)
		assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_RUNNING, sum.GetRuntimeStatus())
		assert.Nil(t, sum.GetCompletedAt())
		assert.Equal(t, sevAt(3), sum.GetLastUpdatedAt().AsTime())
	})

	t.Run("a change after GetSaveRequest drops the committed summary", func(t *testing.T) {
		t.Parallel()
		s := NewState(testOpts())
		s.AddToHistory(sevStarted(1, ""))
		s.AddToHistory(sevExecStarted(0, "wf", nil))
		save(t, s)

		s.AddToHistory(sevRaised(2))
		_, err := s.GetSaveRequest("wf-1")
		require.NoError(t, err)
		s.AddToHistory(sevSuspended(3))
		s.ResetChangeTracking()
		assert.Nil(t, s.Summary())

		// The next save derives it from the whole history.
		s.AddToHistory(sevRaised(4))
		requireSummary(t, Summarize(s.History, nil), save(t, s))
	})

	t.Run("continue-as-new derives the summary again", func(t *testing.T) {
		t.Parallel()
		s := NewState(testOpts())
		s.AddToHistory(sevStarted(1, "v1"))
		s.AddToHistory(sevExecStarted(0, "wf", nil))
		s.AddToHistory(sevTask(2, 0))
		save(t, s)

		// As the orchestrator applies a continue-as-new: the history is
		// cleared and the new start event waits in the inbox.
		s.ApplyRuntimeStateChanges(&backend.WorkflowRuntimeState{ContinuedAsNew: true})
		s.ClearInbox()
		s.AddToInbox(sevExecStarted(50, "wf", nil))
		sum := save(t, s)
		requireSummary(t, &backend.BackendWorkflowSummary{
			Name:          "wf",
			RuntimeStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_PENDING,
			CreatedAt:     sevTS(50),
			LastUpdatedAt: sevTS(50),
		}, sum)

		s.ClearInbox()
		s.AddToHistory(sevStarted(51, "v1"))
		s.AddToHistory(sevExecStarted(50, "wf", nil))
		sum = save(t, s)
		assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_RUNNING, sum.GetRuntimeStatus())
		assert.Equal(t, sevAt(50), sum.GetCreatedAt().AsTime())
		assert.Equal(t, sevAt(51), sum.GetStartedAt().AsTime())
	})

	t.Run("reset derives the summary from the new history", func(t *testing.T) {
		t.Parallel()
		s := NewState(testOpts())
		s.AddToHistory(sevStarted(1, ""))
		s.AddToHistory(sevExecStarted(0, "old", nil))
		s.AddToHistory(sevCompleted(2, protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED))
		save(t, s)

		s.Reset()
		s.AddToHistory(sevStarted(11, ""))
		s.AddToHistory(sevExecStarted(10, "new", nil))
		sum := save(t, s)
		requireSummary(t, Summarize(s.History, nil), sum)
		assert.Equal(t, "new", sum.GetName())
		assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_RUNNING, sum.GetRuntimeStatus())
	})

	t.Run("MarkAsTamperFailed writes a FAILED summary", func(t *testing.T) {
		t.Parallel()
		prior := NewState(testOpts())
		prior.AddToHistory(sevStarted(1, ""))
		prior.AddToHistory(sevExecStarted(0, "wf", nil))
		save(t, prior)

		var saved *api.TransactionalRequest
		store := statefake.New().WithTransactionalStateOperationFn(
			func(_ context.Context, _ bool, req *api.TransactionalRequest, _ bool) error {
				saved = req
				return nil
			},
		)
		got, err := MarkAsTamperFailed(t.Context(), store, "wf-1", testOpts(), prior, errors.New("chain broken"))
		require.NoError(t, err)
		require.NotNil(t, saved)
		sum := savedSummary(t, saved)
		assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED, sum.GetRuntimeStatus())
		requireSummary(t, sum, got.Summary())
	})
}

func TestLoadWorkflowState_Summary(t *testing.T) {
	t.Parallel()

	history := []*backend.HistoryEvent{
		sevStarted(1, ""), sevExecStarted(0, "wf", &protos.ParentInstanceInfo{
			WorkflowInstance: &protos.WorkflowInstance{InstanceId: "p"}, AppID: ptr.Of("pa"),
		}),
		sevStalled(2),
	}

	storeFor := func(t *testing.T, meta *backend.BackendWorkflowStateMetadata) *statefake.Fake {
		t.Helper()
		metaBytes, err := proto.Marshal(meta)
		require.NoError(t, err)
		bulk := api.BulkStateResponse{}
		for i, e := range history {
			b, merr := proto.Marshal(e)
			require.NoError(t, merr)
			bulk[fmt.Sprintf("history-%06d", i)] = api.BulkStateEntry{Data: b}
		}
		return statefake.New().
			WithGetFn(func(context.Context, *api.GetStateRequest, bool) (*api.StateResponse, error) {
				return &api.StateResponse{Data: metaBytes, ETag: ptr.Of("e1")}, nil
			}).
			WithGetBulkFn(func(_ context.Context, req *api.GetBulkStateRequest, _ bool) (api.BulkStateResponse, error) {
				out := api.BulkStateResponse{}
				for _, k := range req.Keys {
					out[k] = bulk[k]
				}
				return out, nil
			})
	}

	t.Run("a row without a summary is derived at load", func(t *testing.T) {
		t.Parallel()
		store := storeFor(t, &backend.BackendWorkflowStateMetadata{HistoryLength: uint64(len(history)), Generation: 1})
		s, err := LoadWorkflowState(t.Context(), store, "wf-1", testOpts())
		require.NoError(t, err)
		requireSummary(t, Summarize(history, nil), s.Summary())
		assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_STALLED, s.Summary().GetRuntimeStatus())
	})

	// History signing does not cover the metadata row, so the summary in the
	// row can be changed or stale. The load ignores it, and the next save
	// writes the summary of the history over it.
	rowSummaries := map[string]*backend.BackendWorkflowSummary{
		"tampered": {
			Name:          "tampered",
			RuntimeStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED,
			CreatedAt:     sevTS(0),
			LastUpdatedAt: sevTS(9),
			CompletedAt:   sevTS(9),
		},
		// The summary of the history without its last event.
		"stale": Summarize(history[:2], nil),
	}
	for name, rowSum := range rowSummaries {
		t.Run("a "+name+" row summary is ignored at load and overwritten by the next save", func(t *testing.T) {
			t.Parallel()
			require.False(t, proto.Equal(Summarize(history, nil), rowSum))
			store := storeFor(t, &backend.BackendWorkflowStateMetadata{
				HistoryLength: uint64(len(history)), Generation: 1, Summary: rowSum,
			})
			s, err := LoadWorkflowState(t.Context(), store, "wf-1", testOpts())
			require.NoError(t, err)
			requireSummary(t, Summarize(history, nil), s.Summary())

			// A save without new history events writes the summary of the
			// history.
			s.AddToInbox(sevRaised(3))
			sum := save(t, s)
			requireSummary(t, Summarize(history, nil), sum)
			assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_STALLED, sum.GetRuntimeStatus())

			// A save with new history events advances from it: the progress
			// event clears the stall.
			s.ClearInbox()
			s.AddToHistory(sevRaised(3))
			sum = save(t, s)
			requireSummary(t, Summarize(s.History, nil), sum)
			assert.Equal(t, "wf", sum.GetName())
			assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_RUNNING, sum.GetRuntimeStatus())
			assert.Equal(t, sevAt(3), sum.GetLastUpdatedAt().AsTime())
		})
	}
}
