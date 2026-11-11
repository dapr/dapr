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

package stream

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/diagridio/go-etcd-cron/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
)

func (s *suite) deliver(t *testing.T, req *loops.TriggerRequest) uint64 {
	t.Helper()
	s.streamLoop.Enqueue(req)
	resp, err := s.clientstream.Recv()
	require.NoError(t, err)
	assert.Equal(t, req.Repark != nil, resp.GetPull(), "only a pull dispatch is delivered as pull")
	return resp.GetId()
}

func (s *suite) result(t *testing.T, res *schedulerv1pb.WatchJobsRequestResult) {
	t.Helper()
	require.NoError(t, s.clientstream.Send(&schedulerv1pb.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Result{Result: res},
	}))
}

func (s *suite) nextEvent(t *testing.T) loops.EventNS {
	t.Helper()
	select {
	case <-time.After(time.Second * 5):
		require.Fail(t, "timeout")
		return nil
	case ev := <-s.nsLoop.Events():
		return ev
	}
}

func (s *suite) shutdown(t *testing.T) {
	t.Helper()
	s.closeserver()
	s.streamLoop.Close(new(loops.StreamShutdown))
	s.expectEvent(t, &loops.ConnCloseStream{StreamIDx: 123, Namespace: "ns"})
}

func Test_StreamPull(t *testing.T) {
	t.Parallel()

	t.Run("NO_CAPACITY calls Repark instead of resolving", func(t *testing.T) {
		t.Parallel()
		suite := newSuite(t)

		var reparked atomic.Bool
		var resolved atomic.Pointer[api.TriggerResponseResult]
		id := suite.deliver(t, &loops.TriggerRequest{
			Job:      &internalsv1pb.JobEvent{Name: "pulled"},
			ResultFn: func(r api.TriggerResponseResult) { resolved.Store(&r) },
			Repark:   func() { reparked.Store(true) },
		})
		suite.result(t, &schedulerv1pb.WatchJobsRequestResult{Id: id, Status: schedulerv1pb.WatchJobsRequestResultStatus_NO_CAPACITY})

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.True(c, reparked.Load())
		}, time.Second*5, time.Millisecond*10)
		assert.Nil(t, resolved.Load())

		// Without a repark hook the refusal is a failure.
		suite.roundTrip(t, "plain", id+1, schedulerv1pb.WatchJobsRequestResultStatus_NO_CAPACITY, api.TriggerResponseResult_FAILED)
		suite.shutdown(t)
	})

	t.Run("pull slots are forwarded with the stream's index", func(t *testing.T) {
		t.Parallel()
		suite := newSuite(t)

		require.NoError(t, suite.clientstream.Send(&schedulerv1pb.WatchJobsRequest{
			WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_PullSlots{
				PullSlots: &schedulerv1pb.PullSlots{ActorType: "activity", Free: 3},
			},
		}))
		suite.expectEvent(t, &loops.PullSlotsUpdate{Namespace: "ns", StreamIDx: 123, AppID: "app-id", ActorType: "activity", Free: 3})

		suite.roundTrip(t, "alive", 1, schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS, api.TriggerResponseResult_SUCCESS)
		suite.shutdown(t)
	})

	t.Run("a result by reminder reference is forwarded", func(t *testing.T) {
		t.Parallel()
		suite := newSuite(t)

		ref := &schedulerv1pb.ActorReminderRef{ActorType: "activity", ActorId: "wf::1::0", Name: "run-activity"}
		suite.result(t, &schedulerv1pb.WatchJobsRequestResult{Reminder: ref, Status: schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS})

		ev, ok := suite.nextEvent(t).(*loops.PullResult)
		require.True(t, ok)
		assert.Equal(t, "ns", ev.Namespace)
		assert.Equal(t, uint64(123), ev.StreamIDx)
		assert.Equal(t, "app-id", ev.AppID)
		assert.True(t, proto.Equal(ref, ev.Reminder))
		assert.Equal(t, schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS, ev.Status)

		suite.roundTrip(t, "alive", 1, schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS, api.TriggerResponseResult_SUCCESS)
		suite.shutdown(t)
	})

	t.Run("shutdown orphans pull triggers and resolves the rest", func(t *testing.T) {
		t.Parallel()
		suite := newSuite(t)

		var orphaned atomic.Bool
		var pullResolved, plainResolved atomic.Pointer[api.TriggerResponseResult]
		suite.deliver(t, &loops.TriggerRequest{
			Job:      &internalsv1pb.JobEvent{Name: "pulled"},
			ResultFn: func(r api.TriggerResponseResult) { pullResolved.Store(&r) },
			Orphan:   func() { orphaned.Store(true) },
		})
		suite.deliver(t, &loops.TriggerRequest{
			Job:      &internalsv1pb.JobEvent{Name: "plain"},
			ResultFn: func(r api.TriggerResponseResult) { plainResolved.Store(&r) },
		})

		suite.shutdown(t)
		assert.True(t, orphaned.Load())
		assert.Nil(t, pullResolved.Load())
		ptr := plainResolved.Load()
		require.NotNil(t, ptr)
		assert.Equal(t, api.TriggerResponseResult_UNDELIVERABLE, *ptr)
	})
}
