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

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

// Stream is a WatchJobs stream standing in for a sidecar: the test receives
// each trigger and answers it explicitly.
type Stream struct {
	t      *testing.T
	stream schedulerv1pb.Scheduler_WatchJobsClient
	ch     chan *schedulerv1pb.WatchJobsResponse
	cancel context.CancelFunc
}

func (s *Scheduler) WatchJobsStream(t *testing.T, ctx context.Context, initial *schedulerv1pb.WatchJobsRequestInitial) *Stream {
	t.Helper()

	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	stream, err := s.Client(t, ctx).WatchJobs(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&schedulerv1pb.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Initial{Initial: initial},
	}))

	ch := make(chan *schedulerv1pb.WatchJobsResponse, 10)
	go func() {
		defer close(ch)
		for {
			resp, err := stream.Recv()
			if err != nil {
				return
			}
			ch <- resp
		}
	}()

	return &Stream{t: t, stream: stream, ch: ch, cancel: cancel}
}

// Recv returns the next trigger delivered on the stream.
func (s *Stream) Recv() *schedulerv1pb.WatchJobsResponse {
	s.t.Helper()
	select {
	case resp, ok := <-s.ch:
		require.True(s.t, ok, "stream closed")
		return resp
	case <-time.After(time.Second * 10):
		require.Fail(s.t, "timed out waiting for a trigger")
		return nil
	}
}

// NoRecv fails if a trigger is delivered on the stream within d.
func (s *Stream) NoRecv(d time.Duration) {
	s.t.Helper()
	select {
	case resp, ok := <-s.ch:
		if ok {
			require.Fail(s.t, "unexpected trigger", resp.GetName())
		}
	case <-time.After(d):
	}
}

func (s *Stream) Result(id uint64, status schedulerv1pb.WatchJobsRequestResultStatus) {
	s.t.Helper()
	s.send(&schedulerv1pb.WatchJobsRequest{WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Result{
		Result: &schedulerv1pb.WatchJobsRequestResult{Id: id, Status: status},
	}})
}

func (s *Stream) PullSlots(actorType string, free uint32) {
	s.t.Helper()
	s.send(&schedulerv1pb.WatchJobsRequest{WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_PullSlots{
		PullSlots: &schedulerv1pb.PullSlots{ActorType: actorType, Free: free},
	}})
}

func (s *Stream) Close() {
	s.cancel()
}

func (s *Stream) send(req *schedulerv1pb.WatchJobsRequest) {
	s.t.Helper()
	require.NoError(s.t, s.stream.Send(req))
}
