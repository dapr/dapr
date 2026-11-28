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

package activity

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/activity/inflight"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/backend"
)

func workItemDurable(t *testing.T, wi *backend.ActivityWorkItem) *todo.Durable {
	t.Helper()
	durable, ok := wi.Properties[todo.DurableProperty].(*todo.Durable)
	require.True(t, ok, "the work item must carry a Durable")
	return durable
}

func requireSettled(t *testing.T, durable *todo.Durable) error {
	t.Helper()
	select {
	case <-durable.Done():
		return durable.Err()
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the execution's Durable was not settled")
		return nil
	}
}

func Test_executeActivity_settlesDurableAfterPublish(t *testing.T) {
	t.Parallel()

	t.Run("settled only once the result is published", func(t *testing.T) {
		t.Parallel()
		f, scheduled := newExecHarness(t)

		publishing := make(chan struct{})
		release := make(chan struct{})
		f.router = routerfake.New().WithCallFn(func(context.Context, *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
			close(publishing)
			<-release
			return &internalsv1pb.InternalInvokeResponse{}, nil
		})

		a := f.GetOrCreate("wf::3::0").(*activity)
		ownerErr := make(chan error, 1)
		go func() {
			ownerErr <- a.executeActivity(t.Context(), testReminder(), testInvocation())
		}()

		wi := recvWorkItem(t, scheduled)
		durable := workItemDurable(t, wi)
		completeWorkItem(t, wi)

		<-publishing
		select {
		case <-durable.Done():
			require.FailNow(t, "the Durable was settled before the publish returned")
		case <-time.After(200 * time.Millisecond):
		}

		close(release)
		require.NoError(t, requireSettled(t, durable))
		require.NoError(t, <-ownerErr)
	})

	t.Run("a failed publish settles with its error", func(t *testing.T) {
		t.Parallel()
		f, scheduled := newExecHarness(t)

		f.router = routerfake.New().WithCallFn(func(context.Context, *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
			return nil, errors.New("parent workflow unreachable")
		})

		a := f.GetOrCreate("wf::3::0").(*activity)
		ownerErr := make(chan error, 1)
		go func() {
			ownerErr <- a.executeActivity(t.Context(), testReminder(), testInvocation())
		}()

		wi := recvWorkItem(t, scheduled)
		durable := workItemDurable(t, wi)
		completeWorkItem(t, wi)

		settled := requireSettled(t, durable)
		require.Error(t, settled)
		assert.Contains(t, settled.Error(), "parent workflow unreachable")
		require.Error(t, <-ownerErr)
	})

	t.Run("a work item the engine does not take settles with an error", func(t *testing.T) {
		t.Parallel()
		f, _ := newExecHarness(t)

		durables := make(chan *todo.Durable, 1)
		f.scheduler = func(_ context.Context, wi *backend.ActivityWorkItem) error {
			durables <- workItemDurable(t, wi)
			return errors.New("engine is not running")
		}

		a := f.GetOrCreate("wf::3::0").(*activity)
		require.Error(t, a.executeActivity(t.Context(), testReminder(), testInvocation()))
		assert.Error(t, requireSettled(t, <-durables))
	})

	t.Run("an evicted claim settles with the eviction error", func(t *testing.T) {
		t.Parallel()
		f, scheduled := newExecHarness(t)
		f.executionHeld = func(string, int32) bool { return false }
		f.staleClaimAfter = time.Millisecond

		a := f.GetOrCreate("wf::3::0").(*activity)
		go func() {
			_ = a.executeActivity(t.Context(), testReminder(), testInvocation())
		}()
		durable := workItemDurable(t, recvWorkItem(t, scheduled))

		key := inflight.Key(a.actorID, testInvocation().GetHistoryEvent())
		require.Eventually(t, func() bool {
			call, owner, err := a.claim(t.Context(), key, "wf", 3, false)
			require.NoError(t, err)
			if owner {
				a.inflight.Release(key, call)
			}
			return owner
		}, 5*time.Second, 5*time.Millisecond)

		require.ErrorIs(t, requireSettled(t, durable), errStaleClaimEvicted)
	})
}
