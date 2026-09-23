/*
Copyright 2026 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://wwb.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package workflow

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/client"
)

func WaitForWorkflowStartedEvent(t *testing.T, ctx context.Context, client *client.TaskHubGrpcClient, id api.InstanceID) {
	t.Helper()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		count := CountHistoryEventsOfType[protos.HistoryEvent_WorkflowStarted](t, ctx, client, id)
		require.Equal(c, 1, count)
	}, 20*time.Second, 10*time.Millisecond)
}

func WaitForRuntimeStatus(t *testing.T, ctx context.Context, client *client.TaskHubGrpcClient, id api.InstanceID, status protos.OrchestrationStatus) {
	t.Helper()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		md, err := client.FetchWorkflowMetadata(ctx, id)
		require.NoError(c, err)
		require.Equal(c, status.String(), md.RuntimeStatus.String())
	}, 20*time.Second, 10*time.Millisecond)
}

func GetLastHistoryEventOfType[T any](t *testing.T, ctx context.Context, client *client.TaskHubGrpcClient, id api.InstanceID) *protos.HistoryEvent {
	t.Helper()
	hist, err := client.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	for i := len(hist.Events) - 1; i >= 0; i-- {
		t := hist.Events[i].GetEventType()
		_, ok := t.(any).(*T)
		if ok {
			return hist.Events[i]
		}
	}
	return nil
}

func CountHistoryEventsOfType[T any](t *testing.T, ctx context.Context, client *client.TaskHubGrpcClient, id api.InstanceID) int {
	t.Helper()
	hist, err := client.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	count := 0
	for _, event := range hist.Events {
		_, ok := event.GetEventType().(any).(*T)
		if ok {
			count++
		}
	}
	return count
}

// ChildCompletions counts the child completion and failure events for taskID
// in the instance's history.
func ChildCompletions(t *testing.T, ctx context.Context, client *client.TaskHubGrpcClient, id api.InstanceID, taskID int32) (completed, failed int) {
	t.Helper()
	hist, err := client.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	for _, e := range hist.GetEvents() {
		if c := e.GetChildWorkflowInstanceCompleted(); c != nil && c.GetTaskScheduledId() == taskID {
			completed++
		}
		if f := e.GetChildWorkflowInstanceFailed(); f != nil && f.GetTaskScheduledId() == taskID {
			failed++
		}
	}
	return completed, failed
}

// WaitForAllCompleted waits for every instance to reach COMPLETED and fails
// listing the ones that did not.
func WaitForAllCompleted(t *testing.T, ctx context.Context, client *client.TaskHubGrpcClient, ids ...api.InstanceID) {
	t.Helper()

	var lock sync.Mutex
	var wg sync.WaitGroup
	failed := make(map[api.InstanceID]string)
	for _, id := range ids {
		wg.Add(1)
		go func(id api.InstanceID) {
			defer wg.Done()
			meta, err := client.WaitForWorkflowCompletion(ctx, id)
			lock.Lock()
			defer lock.Unlock()
			switch {
			case err != nil:
				failed[id] = err.Error()
			case meta.GetRuntimeStatus() != protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED:
				failed[id] = meta.GetRuntimeStatus().String()
			}
		}(id)
	}
	wg.Wait()

	assert.Empty(t, failed, "%d of %d instances did not complete", len(failed), len(ids))
}
