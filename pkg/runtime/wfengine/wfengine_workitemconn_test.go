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

	actorsapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/router"
	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
	"github.com/dapr/dapr/pkg/actors/table"
	tablefake "github.com/dapr/dapr/pkg/actors/table/fake"
	"github.com/dapr/dapr/pkg/messages"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	backendactors "github.com/dapr/dapr/pkg/runtime/wfengine/backends/actors"
	"github.com/dapr/dapr/pkg/runtime/wfengine/inprocess"
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

// TestEngine_ReconnectDuringDisconnectSweep covers a worker reconnecting while
// the last worker's disconnect still cancels a waiter on another daprd, a
// call that takes seconds while no daprd hosts the executor actor type. The
// disconnect waits for it without actorRegLock, so the reconnect does not
// wait for it, and the workflow actor types stay registered.
func TestEngine_ReconnectDuringDisconnectSweep(t *testing.T) {
	cancelling := make(chan struct{}, 1)
	release := make(chan struct{})
	routerFake := routerfake.New().
		WithCallFn(func(ctx context.Context, _ *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
			select {
			case cancelling <- struct{}{}:
			default:
			}
			select {
			case <-release:
				return new(internalsv1pb.InternalInvokeResponse), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}).
		WithCallStreamFn(func(ctx context.Context, _ *internalsv1pb.InternalInvokeRequest, _ func(*internalsv1pb.InternalInvokeResponse) (bool, error)) error {
			<-ctx.Done()
			return ctx.Err()
		})
	fa := actorsfake.New().
		WithTable(func(context.Context) (table.Interface, error) {
			return tablefake.New(), nil
		}).
		WithRouter(func(context.Context) (router.Interface, error) {
			return routerFake, nil
		}).
		WithPlacementLookupActor(func(context.Context, *actorsapi.LookupActorRequest) (*actorsapi.LookupActorResponse, error) {
			return &actorsapi.LookupActorResponse{Local: false}, nil
		})

	abackend, err := backendactors.New(backendactors.Options{
		AppID:                     "wfe-test",
		Namespace:                 "default",
		Actors:                    fa,
		ComponentStore:            compstore.New(),
		EnableClusteredDeployment: true,
	})
	require.NoError(t, err)
	wfe := &engine{
		appID:         "wfe-test",
		namespace:     "default",
		actors:        fa,
		backend:       abackend,
		inProcessExec: inprocess.NewExecutor(),
		compStore:     compstore.New(),
	}

	require.NoError(t, wfe.onWorkItemConnection(t.Context()))

	// The executor actor for the key is on another daprd, so the waiter is
	// a watch stream, and its cancellation a call to that actor.
	dereg := wfe.backend.OnWorkflowTaskCompletion(&protos.WorkflowRequest{InstanceId: "sweep-test"},
		func(*protos.WorkflowResponse, error) {})
	t.Cleanup(dereg)

	disconnected := make(chan error, 1)
	go func() {
		disconnected <- wfe.onWorkItemDisconnection(t.Context())
	}()
	select {
	case <-cancelling:
	case <-time.After(5 * time.Second):
		require.Fail(t, "the disconnect did not cancel the waiter")
	}

	reconnected := make(chan error, 1)
	go func() {
		reconnected <- wfe.onWorkItemConnection(t.Context())
	}()
	select {
	case err = <-reconnected:
		require.NoError(t, err)
	case <-time.After(time.Second):
		require.Fail(t, "the reconnect waited for the disconnect's cancellation")
	}

	close(release)
	select {
	case err = <-disconnected:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		require.Fail(t, "the disconnect did not return")
	}

	wfe.actorRegLock.Lock()
	defer wfe.actorRegLock.Unlock()
	assert.Equal(t, int32(1), wfe.getWorkItemsCount.Load())
	assert.True(t, wfe.actorsRegistered, "the disconnect unregistered the types of the reconnected worker")
}

// TestEngine_FallbackWaitsForDisconnectSweep covers runFallback waking while
// the last worker's disconnect still cancels a waiter with actorRegLock
// released. Its reconcile, and stopFallback, leave the types alone until the
// sweep settled: the disconnect releases them then and kicks runFallback. A
// halt before that would wait on turns whose cancellation it took away.
func TestEngine_FallbackWaitsForDisconnectSweep(t *testing.T) {
	cancelling := make(chan struct{}, 1)
	release := make(chan struct{})
	routerFake := routerfake.New().
		WithCallFn(func(ctx context.Context, _ *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
			select {
			case cancelling <- struct{}{}:
			default:
			}
			select {
			case <-release:
				return new(internalsv1pb.InternalInvokeResponse), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}).
		WithCallStreamFn(func(ctx context.Context, _ *internalsv1pb.InternalInvokeRequest, _ func(*internalsv1pb.InternalInvokeResponse) (bool, error)) error {
			<-ctx.Done()
			return ctx.Err()
		})
	fa := actorsfake.New().
		WithTable(func(context.Context) (table.Interface, error) {
			return tablefake.New(), nil
		}).
		WithRouter(func(context.Context) (router.Interface, error) {
			return routerFake, nil
		}).
		WithPlacementLookupActor(func(context.Context, *actorsapi.LookupActorRequest) (*actorsapi.LookupActorResponse, error) {
			return &actorsapi.LookupActorResponse{Local: false}, nil
		})

	abackend, err := backendactors.New(backendactors.Options{
		AppID:                     "wfe-test",
		Namespace:                 "default",
		Actors:                    fa,
		ComponentStore:            compstore.New(),
		EnableClusteredDeployment: true,
	})
	require.NoError(t, err)
	wfe := &engine{
		appID:         "wfe-test",
		namespace:     "default",
		actors:        fa,
		backend:       abackend,
		inProcessExec: inprocess.NewExecutor(),
		compStore:     compstore.New(),
		fallback:      newFallback(nil),
	}
	// runFallback runs, and a worker registered the types with its executor.
	wfe.fallback.runCtx = t.Context()
	wfe.fallback.executorRegistered = true
	wfe.actorsRegistered = true
	wfe.getWorkItemsCount.Store(1)

	// The executor actor for the key is on another daprd, so the waiter is
	// a watch stream, and its cancellation a call to that actor.
	dereg := wfe.backend.OnWorkflowTaskCompletion(&protos.WorkflowRequest{InstanceId: "sweep-test"},
		func(*protos.WorkflowResponse, error) {})
	t.Cleanup(dereg)

	disconnected := make(chan error, 1)
	go func() {
		disconnected <- wfe.onWorkItemDisconnection(t.Context())
	}()
	select {
	case <-cancelling:
	case <-time.After(5 * time.Second):
		require.Fail(t, "the disconnect did not cancel the waiter")
	}

	// runFallback wakes during the sweep, with no other daprd of the app
	// having an executor, and shutdown begins.
	wfe.actorRegLock.Lock()
	wait, err := wfe.reconcileFallback(t.Context(), true, false)
	require.NoError(t, err)
	assert.False(t, wait)
	assert.True(t, wfe.fallback.executorRegistered, "reconcile demoted the types while the sweep was in flight")
	wfe.actorRegLock.Unlock()
	wfe.stopFallback()
	wfe.actorRegLock.Lock()
	assert.True(t, wfe.actorsRegistered, "stopFallback released the types while the sweep was in flight")
	wfe.actorRegLock.Unlock()

	close(release)
	select {
	case err = <-disconnected:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		require.Fail(t, "the disconnect did not return")
	}

	// The disconnect kicked runFallback once its sweep settled, and the
	// reconcile that follows turns the daprd into a fallback host.
	select {
	case <-wfe.fallback.kick:
	default:
		require.Fail(t, "the disconnect did not kick runFallback once its sweep settled")
	}
	wfe.actorRegLock.Lock()
	defer wfe.actorRegLock.Unlock()
	assert.Zero(t, wfe.sweeping)
	_, err = wfe.reconcileFallback(t.Context(), true, false)
	require.NoError(t, err)
	assert.False(t, wfe.fallback.executorRegistered)
	assert.True(t, wfe.actorsRegistered)
}
