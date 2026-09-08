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

package wfengine

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/table"
	tablefake "github.com/dapr/dapr/pkg/actors/table/fake"
	"github.com/dapr/durabletask-go/api/protos"

	actorsfake "github.com/dapr/dapr/pkg/actors/fake"
)

// TestEngine_WorkItemConnectionFailurePairing pins the connect/disconnect
// accounting contract with the grpc executor: the executor invokes the
// disconnect callback exactly once per connect callback invocation, including
// for connections whose connect callback returned an error. The connect error
// path must therefore not decrement getWorkItemsCount itself; doing so drifts
// the count negative permanently, which pins the pending tracker unavailable
// and cancels every later completion registration on arrival even while a
// healthy worker is connected (the churnstrand strand-everything signature).
func TestEngine_WorkItemConnectionFailurePairing(t *testing.T) {
	var tableErr atomic.Bool
	tableErr.Store(true)
	fa := actorsfake.New().WithTable(func(context.Context) (table.Interface, error) {
		if tableErr.Load() {
			return nil, errors.New("placement churn")
		}
		return tablefake.New(), nil
	})

	wfe, _ := newTestEngine(t, fa)

	// A connection whose actor registration fails, then its paired
	// disconnect, exactly as the executor drives them.
	require.Error(t, wfe.onWorkItemConnection(t.Context()))
	require.NoError(t, wfe.onWorkItemDisconnection(t.Context()))
	require.Equal(t, int32(0), wfe.getWorkItemsCount.Load(),
		"a failed connection must net the stream count to zero, not negative")

	// A healthy reconnect must count as one connected worker and restore
	// executor availability.
	tableErr.Store(false)
	require.NoError(t, wfe.onWorkItemConnection(t.Context()))
	require.Equal(t, int32(1), wfe.getWorkItemsCount.Load())

	// With a worker connected, a completion registration must stay armed
	// rather than be cancelled on arrival by the pending tracker.
	cancelled := make(chan error, 1)
	dereg := wfe.backend.OnWorkflowTaskCompletion(
		&protos.WorkflowRequest{InstanceId: "pairing-test"},
		func(_ *protos.WorkflowResponse, err error) {
			cancelled <- err
		})
	t.Cleanup(dereg)

	select {
	case err := <-cancelled:
		t.Fatalf("completion registration cancelled while a worker is connected: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	require.NoError(t, wfe.onWorkItemDisconnection(t.Context()))
	assert.Equal(t, int32(0), wfe.getWorkItemsCount.Load())
}
