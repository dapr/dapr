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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/client"
)

func WaitForOrchestratorStartedEvent(t *testing.T, ctx context.Context, client *client.TaskHubGrpcClient, id api.InstanceID) {
	t.Helper()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		count := CountHistoryEventsOfType[protos.HistoryEvent_OrchestratorStarted](t, ctx, client, id)
		require.Equal(c, 1, count)
	}, 20*time.Second, 10*time.Millisecond)
}

func WaitForRuntimeStatus(t *testing.T, ctx context.Context, client *client.TaskHubGrpcClient, id api.InstanceID, status protos.OrchestrationStatus) {
	t.Helper()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		md, err := client.FetchOrchestrationMetadata(ctx, id)
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
