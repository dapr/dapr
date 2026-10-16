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
	"github.com/dapr/dapr/pkg/messages"
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

// TestEngine_WorkItemDisconnectionStreamCancelled pins that the last worker's
// disconnect removes the workflow actor types even when the transport cancels
// the stream's context during the call. The actors runtime can fail the table
// lookup for a done context, and actorsRegistered is reset either way, so no
// later disconnect would retry: the host would keep the types with no worker
// to run them.
func TestEngine_WorkItemDisconnectionStreamCancelled(t *testing.T) {
	var unregistered atomic.Bool
	tbl := tablefake.New().WithUnRegisterActorTypes(func(...string) error {
		unregistered.Store(true)
		return nil
	})
	fa := actorsfake.New().WithTable(func(ctx context.Context) (table.Interface, error) {
		// The actors runtime returns ErrActorRuntimeNotFound when ctx is
		// done, even if the runtime is ready.
		select {
		case <-ctx.Done():
			return nil, messages.ErrActorRuntimeNotFound
		default:
			return tbl, nil
		}
	})

	wfe, _ := newTestEngine(t, fa)
	require.NoError(t, wfe.onWorkItemConnection(t.Context()))
	require.True(t, wfe.actorsRegistered)

	// The transport closes just after onWorkItemDisconnection checks the
	// stream context: Err still reports nil, but Done is already closed.
	done := make(chan struct{})
	close(done)
	ctx := closingContext{Context: context.Background(), done: done}
	require.NoError(t, wfe.onWorkItemDisconnection(ctx))
	assert.True(t, unregistered.Load(), "the workflow actor types must be removed from the table")
	assert.False(t, wfe.actorsRegistered)
}
