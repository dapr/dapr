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

// Package watchjobs fakes daprd's side of the scheduler WatchJobs stream, so
// scheduler tests can control exactly when triggers are acknowledged.
package watchjobs

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/kit/ptr"
)

// Watcher is a fake daprd WatchJobs stream. Triggers it receives arrive on
// RecvCh.
type Watcher struct {
	stream   schedulerv1.Scheduler_WatchJobsClient
	sendLock sync.Mutex
	RecvCh   chan *schedulerv1.WatchJobsResponse
}

// Watch opens a WatchJobs stream with the given initial request, defaulting
// the namespace to "default" and the accepted job types to actor reminders.
func Watch(t *testing.T, ctx context.Context, client schedulerv1.SchedulerClient, initial *schedulerv1.WatchJobsRequestInitial) *Watcher {
	t.Helper()

	if initial.GetNamespace() == "" {
		initial.Namespace = "default"
	}
	if len(initial.GetAcceptJobTypes()) == 0 {
		initial.AcceptJobTypes = []schedulerv1.JobTargetType{schedulerv1.JobTargetType_JOB_TARGET_TYPE_ACTOR_REMINDER}
	}

	stream, err := client.WatchJobs(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&schedulerv1.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1.WatchJobsRequest_Initial{Initial: initial},
	}))

	w := &Watcher{stream: stream, RecvCh: make(chan *schedulerv1.WatchJobsResponse, 10_000)}
	go func() {
		for {
			resp, rerr := stream.Recv()
			if rerr != nil {
				close(w.RecvCh)
				return
			}
			w.RecvCh <- resp
		}
	}()
	return w
}

// Recv waits up to 10 seconds for the next trigger.
func (w *Watcher) Recv(t *testing.T, msg string) *schedulerv1.WatchJobsResponse {
	t.Helper()
	select {
	case resp, ok := <-w.RecvCh:
		require.True(t, ok, "stream closed waiting for: "+msg)
		return resp
	case <-time.After(10 * time.Second):
		require.Fail(t, "timed out waiting for trigger: "+msg)
		return nil
	}
}

// Ack acknowledges a trigger as successful.
func (w *Watcher) Ack(t *testing.T, resp *schedulerv1.WatchJobsResponse) {
	t.Helper()
	require.NoError(t, w.ack(resp))
}

func (w *Watcher) ack(resp *schedulerv1.WatchJobsResponse) error {
	w.sendLock.Lock()
	defer w.sendLock.Unlock()
	return w.stream.Send(&schedulerv1.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1.WatchJobsRequest_Result{
			Result: &schedulerv1.WatchJobsRequestResult{
				Id:     resp.GetId(),
				Status: schedulerv1.WatchJobsRequestResultStatus_SUCCESS,
			},
		},
	})
}

// ScheduleActor schedules an actor reminder job due now, carrying
// concurrencyKey. A non-nil every makes it repeat at that interval.
func ScheduleActor(t *testing.T, ctx context.Context, s *scheduler.Scheduler, client schedulerv1.SchedulerClient, name, appID, actorType, concurrencyKey string, every *time.Duration) {
	t.Helper()
	req := s.JobNowActor(name, "default", appID, actorType, name)
	if concurrencyKey != "" {
		req.Metadata.ConcurrencyKey = ptr.Of(concurrencyKey)
	}
	if every != nil {
		req.Job.Schedule = ptr.Of("@every " + every.String())
	}
	_, err := client.ScheduleJob(ctx, req)
	require.NoError(t, err)
}

// Metric returns the sum of a scheduler metric's series whose concurrency_key
// label is exactly key.
func Metric(c assert.TestingT, ctx context.Context, s *scheduler.Scheduler, name, key string) float64 {
	return s.Metrics(c, ctx).SumWithLabels(name, "concurrency_key:"+key)
}

// WaitPending waits until n triggers of key are waiting for a slot.
func WaitPending(t *testing.T, ctx context.Context, s *scheduler.Scheduler, key string, n int) {
	t.Helper()
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.InDelta(c, float64(n), Metric(c, ctx, s, "dapr_scheduler_concurrency_pending", key), 0)
	}, 10*time.Second, 10*time.Millisecond)
}

// LimitActor is a concurrency limit for an actor type, or for one name of it.
func LimitActor(actorType string, name *string, maxConcurrent int32) *schedulerv1.ConcurrencyLimit {
	return &schedulerv1.ConcurrencyLimit{
		Target:        &schedulerv1.ConcurrencyLimit_Actor{Actor: &schedulerv1.ConcurrencyLimitActor{Type: actorType}},
		Name:          name,
		MaxConcurrent: maxConcurrent,
	}
}

// Server simulates the capacity of daprd sidecars: a fixed number of workers
// shared by every stream fed to it, each taking a fixed time to acknowledge a
// trigger. Triggers wait in FIFO order for a worker.
type Server struct {
	queue chan served
}

type served struct {
	w    *Watcher
	resp *schedulerv1.WatchJobsResponse
}

func NewServer(ctx context.Context, workers int, service time.Duration) *Server {
	s := &Server{queue: make(chan served, 100_000)}
	for range workers {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case item := <-s.queue:
					time.Sleep(service)
					_ = item.w.ack(item.resp)
				}
			}
		}()
	}
	return s
}

// Serve feeds every trigger the watcher receives to the server, calling
// onRecv for each first.
func (s *Server) Serve(ctx context.Context, w *Watcher, onRecv func(*schedulerv1.WatchJobsResponse)) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case resp, ok := <-w.RecvCh:
				if !ok {
					return
				}
				if onRecv != nil {
					onRecv(resp)
				}
				s.queue <- served{w: w, resp: resp}
			}
		}
	}()
}
