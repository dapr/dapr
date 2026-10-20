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

package actors

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	inmemory "github.com/dapr/components-contrib/state/in-memory"
	actorsfake "github.com/dapr/dapr/pkg/actors/fake"
	"github.com/dapr/dapr/pkg/actors/router"
	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/kit/logger"
)

// newMarkerTestBackend returns a backend whose actor calls all get a
// response with the fallback header set as given.
func newMarkerTestBackend(t *testing.T, fallback bool) *Actors {
	t.Helper()

	cs := compstore.New()
	require.NoError(t, cs.AddStateStoreActor("mystore", inmemory.NewInMemoryStateStore(logger.NewLogger(t.Name()))))

	r := routerfake.New().WithCallFn(func(context.Context, *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
		res := new(internalsv1pb.InternalInvokeResponse)
		if fallback {
			common.SetFallback(res)
		}
		return res, nil
	})

	return &Actors{
		appID:             "myapp",
		workflowActorType: "dapr.internal.default.myapp.workflow",
		compStore:         cs,
		actors: actorsfake.New().WithRouter(func(context.Context) (router.Interface, error) {
			return r, nil
		}),
	}
}

func TestFallbackMarker(t *testing.T) {
	t.Parallel()

	rerun := func() *backend.RerunWorkflowFromEventRequest {
		return &backend.RerunWorkflowFromEventRequest{
			SourceInstanceID: "src",
			NewInstanceID:    new("dst"),
		}
	}
	never := func(*backend.WorkflowMetadata) bool { return false }

	t.Run("a rerun served by a fallback host marks the ctx", func(t *testing.T) {
		t.Parallel()
		abe := newMarkerTestBackend(t, true)

		ctx := WithFallbackMarker(t.Context())
		id, err := abe.RerunWorkflowFromEvent(ctx, rerun())
		require.NoError(t, err)
		assert.Equal(t, "dst", string(id))
		assert.True(t, servedByFallback(ctx))

		// So the executor's wait for the new instance returns at once.
		require.NoError(t, abe.WatchWorkflowRuntimeStatus(ctx, id, nil, never))
	})

	t.Run("an event served by a fallback host marks the ctx", func(t *testing.T) {
		t.Parallel()
		abe := newMarkerTestBackend(t, true)

		ctx := WithFallbackMarker(t.Context())
		require.NoError(t, abe.AddNewWorkflowEvent(ctx, "wf1", &backend.HistoryEvent{
			EventType: &protos.HistoryEvent_ExecutionSuspended{ExecutionSuspended: &protos.ExecutionSuspendedEvent{}},
		}))
		assert.True(t, servedByFallback(ctx))
	})

	t.Run("an operation served by a host with an executor leaves the ctx unmarked", func(t *testing.T) {
		t.Parallel()
		abe := newMarkerTestBackend(t, false)

		ctx := WithFallbackMarker(t.Context())
		_, err := abe.RerunWorkflowFromEvent(ctx, rerun())
		require.NoError(t, err)
		assert.False(t, servedByFallback(ctx))
	})

	t.Run("a ctx without a marker is never marked", func(t *testing.T) {
		t.Parallel()
		abe := newMarkerTestBackend(t, true)
		_, err := abe.RerunWorkflowFromEvent(t.Context(), rerun())
		require.NoError(t, err)
		assert.False(t, servedByFallback(t.Context()))
	})
}
