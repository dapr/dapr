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

package cluster

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/runtime/scheduler/internal/cluster/fake"
	"github.com/dapr/kit/concurrency"
)

func TestConnectorRunRetriesOnWatchJobsError(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	ctx, cancel := context.WithCancel(t.Context())
	client := fake.NewClient().WithWatchJobs(
		func(ctx context.Context, opts ...grpc.CallOption) (schedulerv1pb.Scheduler_WatchJobsClient, error) {
			n := attempts.Add(1)
			if n <= 3 {
				return nil, errors.New("connection refused")
			}
			cancel()
			return nil, ctx.Err()
		},
	)

	c := &connector{
		client: client,
		req:    &schedulerv1pb.WatchJobsRequest{},
	}

	err := c.run(ctx)
	require.ErrorIs(t, err, context.Canceled)
	assert.GreaterOrEqual(t, attempts.Load(), int32(3),
		"connector should have retried WatchJobs at least 3 times")
}

func TestConnectorRunRetriesOnSendError(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	ctx, cancel := context.WithCancel(t.Context())
	client := fake.NewClient().WithWatchJobs(
		func(ctx context.Context, opts ...grpc.CallOption) (schedulerv1pb.Scheduler_WatchJobsClient, error) {
			n := attempts.Add(1)
			if n >= 3 {
				cancel()
				return nil, ctx.Err()
			}
			return fake.NewStream().
				WithContext(ctx).
				WithSend(func(*schedulerv1pb.WatchJobsRequest) error {
					return errors.New("send failed")
				}), nil
		},
	)

	c := &connector{
		client: client,
		req:    &schedulerv1pb.WatchJobsRequest{},
	}

	err := c.run(ctx)
	require.ErrorIs(t, err, context.Canceled)
	assert.GreaterOrEqual(t, attempts.Load(), int32(3),
		"connector should have retried after Send error")
}

func TestConnectorRunRetriesOnStreamDisconnect(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	ctx, cancel := context.WithCancel(t.Context())
	client := fake.NewClient().WithWatchJobs(
		func(ctx context.Context, opts ...grpc.CallOption) (schedulerv1pb.Scheduler_WatchJobsClient, error) {
			n := attempts.Add(1)
			if n >= 3 {
				cancel()
				return nil, ctx.Err()
			}
			return fake.NewStream().
				WithContext(ctx).
				WithRecv(func() (*schedulerv1pb.WatchJobsResponse, error) {
					return nil, io.EOF
				}), nil
		},
	)

	c := &connector{
		client: client,
		req:    &schedulerv1pb.WatchJobsRequest{},
	}

	err := c.run(ctx)
	require.ErrorIs(t, err, context.Canceled)
	assert.GreaterOrEqual(t, attempts.Load(), int32(3),
		"connector should have retried after stream disconnect")
}

func TestConnectorRunReturnsOnContextCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	client := fake.NewClient().WithWatchJobs(
		func(ctx context.Context, opts ...grpc.CallOption) (schedulerv1pb.Scheduler_WatchJobsClient, error) {
			return fake.NewStream().WithContext(ctx), nil
		},
	)

	c := &connector{
		client: client,
		req:    &schedulerv1pb.WatchJobsRequest{},
	}

	errCh := make(chan error, 1)
	go func() { errCh <- c.run(ctx) }()

	cancel()

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("connector.run did not return after context cancel")
	}
}

func TestConnectorFailureDoesNotKillSiblings(t *testing.T) {
	t.Parallel()

	// This test verifies the core bug fix: a single connector's failure should
	// not tear down sibling connectors running in the same RunnerManager.

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var failingAttempts atomic.Int32
	var healthyRecvCount atomic.Int32

	failingClient := fake.NewClient().WithWatchJobs(
		func(ctx context.Context, opts ...grpc.CallOption) (schedulerv1pb.Scheduler_WatchJobsClient, error) {
			failingAttempts.Add(1)
			return nil, errors.New("scheduler unavailable")
		},
	)

	healthyClient := fake.NewClient().WithWatchJobs(
		func(ctx context.Context, opts ...grpc.CallOption) (schedulerv1pb.Scheduler_WatchJobsClient, error) {
			return fake.NewStream().
				WithContext(ctx).
				WithRecv(func() (*schedulerv1pb.WatchJobsResponse, error) {
					healthyRecvCount.Add(1)
					<-ctx.Done()
					return nil, ctx.Err()
				}), nil
		},
	)

	req := &schedulerv1pb.WatchJobsRequest{}
	failingConnector := &connector{client: failingClient, req: req}
	healthyConnector := &connector{client: healthyClient, req: req}

	errCh := make(chan error, 1)
	go func() {
		errCh <- concurrency.NewRunnerManager(
			failingConnector.run,
			healthyConnector.run,
		).Run(ctx)
	}()

	// Wait for the failing connector to retry several times while the healthy
	// one stays connected.
	require.Eventually(t, func() bool {
		return failingAttempts.Load() >= 3
	}, 10*time.Second, 100*time.Millisecond,
		"failing connector should keep retrying")

	// The healthy connector should still be connected (its Recv was called).
	assert.Equal(t, int32(1), healthyRecvCount.Load(),
		"healthy connector should have called Recv exactly once (blocking)")

	// Clean shutdown.
	cancel()
	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("RunnerManager did not exit after cancel")
	}
}
