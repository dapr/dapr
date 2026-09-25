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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/activity/inflight"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/durabletask-go/api"
)

// Test_publishResult_retriesRefusalInHand pins that a refused result publish
// is retried with the result in hand instead of surfacing to the reminder
// chain, which would re-execute the body for a result that already exists.
func Test_publishResult_retriesRefusalInHand(t *testing.T) {
	t.Parallel()

	key := inflight.Key("wf::3::0", testInvocation().GetHistoryEvent())

	t.Run("a refusal that clears is delivered once", func(t *testing.T) {
		t.Parallel()
		f, scheduled := newExecHarness(t)
		f.publishRetryWindow = 10 * time.Second
		var calls atomic.Int32
		f.router = routerfake.New().WithCallFn(func(context.Context, *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
			if calls.Add(1) < 3 {
				return nil, errors.New("task 3: " + common.ErrSchedulingNotDurable.Error())
			}
			return &internalsv1pb.InternalInvokeResponse{}, nil
		})

		a := f.GetOrCreate("wf::3::0").(*activity)
		ownerErr := make(chan error, 1)
		go func() {
			ownerErr <- a.executeActivity(t.Context(), testReminder(), testInvocation())
		}()
		wi := recvWorkItem(t, scheduled)
		call, ok := f.inflight.Peek(key)
		require.True(t, ok)

		completeWorkItem(t, wi)
		require.NoError(t, <-ownerErr)
		f.detached.Wait()

		assert.Equal(t, int32(3), calls.Load(), "the publish must be retried until accepted")
		require.True(t, call.Settled())
		require.NoError(t, call.Err(), "the entry caches the success so a retry acks instead of re-executing")
		assert.Empty(t, scheduled, "the body must not run again")
	})

	t.Run("an unknown instance is terminal at once", func(t *testing.T) {
		t.Parallel()
		f, scheduled := newExecHarness(t)
		f.publishRetryWindow = 10 * time.Second
		var calls atomic.Int32
		f.router = routerfake.New().WithCallFn(func(context.Context, *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
			calls.Add(1)
			return nil, errors.New("wrapped: " + api.ErrInstanceNotFound.Error())
		})

		a := f.GetOrCreate("wf::3::0").(*activity)
		ownerErr := make(chan error, 1)
		go func() {
			ownerErr <- a.executeActivity(t.Context(), testReminder(), testInvocation())
		}()
		wi := recvWorkItem(t, scheduled)
		completeWorkItem(t, wi)
		require.NoError(t, <-ownerErr, "a dropped completion is acked so the sender stops")
		f.detached.Wait()
		assert.Equal(t, int32(1), calls.Load())
	})

	t.Run("a generic refusal is not retried", func(t *testing.T) {
		t.Parallel()
		f, scheduled := newExecHarness(t)
		f.publishRetryWindow = 10 * time.Second
		var calls atomic.Int32
		f.router = routerfake.New().WithCallFn(func(context.Context, *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
			calls.Add(1)
			return nil, errors.New("parent unreachable")
		})

		a := f.GetOrCreate("wf::3::0").(*activity)
		ownerErr := make(chan error, 1)
		go func() {
			ownerErr <- a.executeActivity(t.Context(), testReminder(), testInvocation())
		}()
		wi := recvWorkItem(t, scheduled)
		completeWorkItem(t, wi)
		require.ErrorContains(t, <-ownerErr, "parent unreachable")
		f.detached.Wait()
		assert.Equal(t, int32(1), calls.Load(), "only the not-yet-durable refusal is retried in hand")
	})

	t.Run("a superseded verdict that outlasts the window is dropped", func(t *testing.T) {
		t.Parallel()
		f, scheduled := newExecHarness(t)
		f.publishRetryWindow = 200 * time.Millisecond
		var calls atomic.Int32
		f.router = routerfake.New().WithCallFn(func(context.Context, *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
			calls.Add(1)
			return nil, errors.New("task 3: " + common.ErrSchedulingSuperseded.Error())
		})

		a := f.GetOrCreate("wf::3::0").(*activity)
		ownerErr := make(chan error, 1)
		go func() {
			ownerErr <- a.executeActivity(t.Context(), testReminder(), testInvocation())
		}()
		wi := recvWorkItem(t, scheduled)
		completeWorkItem(t, wi)
		require.NoError(t, <-ownerErr, "a straggler still superseded after the window is dropped, not re-executed")
		f.detached.Wait()
		assert.GreaterOrEqual(t, calls.Load(), int32(2), "the publish must have been retried within the window")
	})

	t.Run("a delivery accepted at the window's edge is not reported as the refusal", func(t *testing.T) {
		t.Parallel()
		f, scheduled := newExecHarness(t)
		f.publishRetryWindow = 500 * time.Millisecond
		var calls atomic.Int32
		f.router = routerfake.New().WithCallFn(func(ctx context.Context, _ *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
			// Refuse once, then accept from inside a call the window cuts:
			// the acceptance is what the sender must act on, not the refusal
			// it happens to remember.
			if calls.Add(1) == 1 {
				return nil, errors.New("task 3: " + common.ErrSchedulingSuperseded.Error())
			}
			<-ctx.Done()
			return nil, nil
		})

		a := f.GetOrCreate("wf::3::0").(*activity)
		ownerErr := make(chan error, 1)
		go func() {
			ownerErr <- a.executeActivity(t.Context(), testReminder(), testInvocation())
		}()
		wi := recvWorkItem(t, scheduled)
		completeWorkItem(t, wi)
		require.NoError(t, <-ownerErr, "an accepted delivery must settle the execution as a success")
		f.detached.Wait()
		assert.Equal(t, int32(2), calls.Load())
	})

	t.Run("a refusal that outlasts the window surfaces for recovery", func(t *testing.T) {
		t.Parallel()
		f, scheduled := newExecHarness(t)
		f.publishRetryWindow = 200 * time.Millisecond
		var calls atomic.Int32
		f.router = routerfake.New().WithCallFn(func(context.Context, *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
			calls.Add(1)
			return nil, errors.New("task 3: " + common.ErrSchedulingNotDurable.Error())
		})

		a := f.GetOrCreate("wf::3::0").(*activity)
		ownerErr := make(chan error, 1)
		go func() {
			ownerErr <- a.executeActivity(t.Context(), testReminder(), testInvocation())
		}()
		wi := recvWorkItem(t, scheduled)
		completeWorkItem(t, wi)
		require.ErrorContains(t, <-ownerErr, common.ErrSchedulingNotDurable.Error())
		f.detached.Wait()
		assert.GreaterOrEqual(t, calls.Load(), int32(2), "the publish must have been retried within the window")
		_, ok := f.inflight.Peek(key)
		assert.False(t, ok, "a publish that keeps failing releases the entry so the retry re-executes as a fresh owner")
	})
}
