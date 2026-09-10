/*
Copyright 2025 The Dapr Authors
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
	"context"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/diagridio/go-etcd-cron/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	apifake "github.com/diagridio/go-etcd-cron/tests/framework/fake"

	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	serverfake "github.com/dapr/dapr/pkg/scheduler/server/fake"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops/fake"
	"github.com/dapr/kit/events/loop"
)

type suite struct {
	srv          *serverfake.Fake
	nsLoop       *fake.Fake
	streamLoop   loop.Interface[loops.EventStream]
	clientstream schedulerv1pb.Scheduler_WatchJobsClient
	serverstream schedulerv1pb.Scheduler_WatchJobsServer
	closeserver  context.CancelFunc
}

func newSuite(t *testing.T) *suite {
	t.Helper()

	srv := serverfake.New(t)

	serstreamCh := make(chan schedulerv1pb.Scheduler_WatchJobsServer, 1)
	ctx, cancel := context.WithCancel(t.Context())
	srv.WithWatchJobs(func(serstream schedulerv1pb.Scheduler_WatchJobsServer) error {
		serstreamCh <- serstream
		<-ctx.Done()
		return nil
	})

	clstream, err := srv.Client().WatchJobs(t.Context())
	require.NoError(t, err)

	nsLoop := fake.New(t)

	serverstream := <-serstreamCh

	streamLoop, err := New(t.Context(), Options{
		IDx: 123,
		Add: &loops.ConnAdd{
			Channel: serverstream,
			Request: &schedulerv1pb.WatchJobsRequestInitial{
				AppId:     "app-id",
				Namespace: "ns",
			},
			Cancel: func(error) {},
		},
		NamespaceLoop: nsLoop.Loop(),
		Cron:          apifake.New(),
	})
	require.NoError(t, err)

	errCh := make(chan error)
	go func() { errCh <- streamLoop.Run(t.Context()) }()
	t.Cleanup(func() {
		select {
		case <-time.After(time.Second * 5):
			require.Fail(t, "timeout")
		case err := <-errCh:
			require.NoError(t, err)
		}
	})

	return &suite{
		srv:          srv,
		nsLoop:       nsLoop,
		streamLoop:   streamLoop,
		clientstream: clstream,
		serverstream: serverstream,
		closeserver:  cancel,
	}
}

func (s *suite) expectEvent(t *testing.T, e loops.EventNS) {
	t.Helper()

	select {
	case <-time.After(time.Second * 5):
		require.Fail(t, "timeout")
	case ev := <-s.nsLoop.Events():
		assert.Equal(t, e, ev)
	}
}

func (s *suite) expectNoEvent(t *testing.T) {
	t.Helper()

	select {
	case ev := <-s.nsLoop.Events():
		require.Failf(t, "unexpected namespace event", "%v", ev)
	default:
	}
}

// roundTrip drives one trigger through the stream and asserts it resolves
// with the given ack status, proving the stream loop is still processing
// both directions.
func (s *suite) roundTrip(t *testing.T, name string, id uint64, status schedulerv1pb.WatchJobsRequestResultStatus, exp api.TriggerResponseResult) {
	t.Helper()

	var called atomic.Pointer[api.TriggerResponseResult]
	s.streamLoop.Enqueue(&loops.TriggerRequest{
		Job: &internalsv1pb.JobEvent{
			Key: name, Name: name,
		},
		ResultFn: func(res api.TriggerResponseResult) { called.Store(&res) },
	})

	req, err := s.clientstream.Recv()
	require.NoError(t, err)
	assert.True(t, proto.Equal(&schedulerv1pb.WatchJobsResponse{Name: name, Id: id}, req), "unexpected delivery: %v", req)

	require.NoError(t, s.clientstream.Send(&schedulerv1pb.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Result{
			Result: &schedulerv1pb.WatchJobsRequestResult{
				Id:     id,
				Status: status,
			},
		},
	}))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		ptr := called.Load()
		if assert.NotNil(c, ptr) {
			assert.Equal(c, exp, *ptr)
		}
	}, time.Second*10, time.Millisecond*10)
}

func Test_Stream(t *testing.T) {
	t.Parallel()

	t.Run("close client stream should close steam", func(t *testing.T) {
		t.Parallel()

		suite := newSuite(t)

		require.NoError(t, suite.clientstream.CloseSend())
		suite.expectEvent(t, &loops.ConnCloseStream{
			StreamIDx: 123,
			Namespace: "ns",
		})
		suite.streamLoop.Close(new(loops.StreamShutdown))
	})

	t.Run("result for unknown id is dropped and the stream stays alive", func(t *testing.T) {
		t.Parallel()

		suite := newSuite(t)

		require.NoError(t, suite.clientstream.Send(&schedulerv1pb.WatchJobsRequest{
			WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Result{
				Result: &schedulerv1pb.WatchJobsRequestResult{
					Id:     999,
					Status: schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS,
				},
			},
		}))

		// A later trigger still resolves through the same stream: the
		// unknown result was dropped instead of tearing the stream down.
		suite.roundTrip(t, "job-alive", 1, schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS, api.TriggerResponseResult_SUCCESS)
		suite.expectNoEvent(t)

		suite.closeserver()
		suite.streamLoop.Close(new(loops.StreamShutdown))
		suite.expectEvent(t, &loops.ConnCloseStream{StreamIDx: 123, Namespace: "ns"})
	})

	t.Run("duplicate ack is dropped and the stream stays alive", func(t *testing.T) {
		t.Parallel()

		suite := newSuite(t)

		suite.roundTrip(t, "job-1", 1, schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS, api.TriggerResponseResult_SUCCESS)

		// Client retries the ack for the already-resolved trigger.
		require.NoError(t, suite.clientstream.Send(&schedulerv1pb.WatchJobsRequest{
			WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Result{
				Result: &schedulerv1pb.WatchJobsRequestResult{
					Id:     1,
					Status: schedulerv1pb.WatchJobsRequestResultStatus_FAILED,
				},
			},
		}))

		suite.roundTrip(t, "job-2", 2, schedulerv1pb.WatchJobsRequestResultStatus_FAILED, api.TriggerResponseResult_FAILED)
		suite.expectNoEvent(t)

		suite.closeserver()
		suite.streamLoop.Close(new(loops.StreamShutdown))
		suite.expectEvent(t, &loops.ConnCloseStream{StreamIDx: 123, Namespace: "ns"})
	})

	t.Run("receiving an initial request from client should close stream", func(t *testing.T) {
		t.Parallel()

		suite := newSuite(t)

		require.NoError(t, suite.clientstream.Send(&schedulerv1pb.WatchJobsRequest{
			WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Initial{
				Initial: &schedulerv1pb.WatchJobsRequestInitial{
					AppId:     "app-id",
					Namespace: "ns",
				},
			},
		}))

		suite.expectEvent(t, &loops.ConnCloseStream{
			StreamIDx: 123, Namespace: "ns",
		})

		suite.streamLoop.Close(new(loops.StreamShutdown))
	})

	t.Run("receiving multiple trigger responses and trigger requests which have callbacks", func(t *testing.T) {
		t.Parallel()

		suite := newSuite(t)

		var called1, called2, called3 atomic.Pointer[api.TriggerResponseResult]

		suite.streamLoop.Enqueue(&loops.TriggerRequest{
			Job: &internalsv1pb.JobEvent{
				Key: "job-key-1", Name: "job-name-1",
			},
			ResultFn: func(res api.TriggerResponseResult) { called1.Store(&res) },
		})
		suite.streamLoop.Enqueue(&loops.TriggerRequest{
			Job: &internalsv1pb.JobEvent{
				Key: "job-key-2", Name: "job-name-2",
			},
			ResultFn: func(res api.TriggerResponseResult) { called2.Store(&res) },
		})
		suite.streamLoop.Enqueue(&loops.TriggerRequest{
			Job: &internalsv1pb.JobEvent{
				Key: "job-key-3", Name: "job-name-3",
			},
			ResultFn: func(res api.TriggerResponseResult) { called3.Store(&res) },
		})

		for i := range uint64(3) {
			req, err := suite.clientstream.Recv()
			require.NoError(t, err)
			exp := &schedulerv1pb.WatchJobsResponse{
				Name: "job-name-" + strconv.FormatUint(i+1, 10),
				Id:   i + 1,
			}
			assert.True(t, proto.Equal(exp, req), "%v != %v", exp, req)
		}

		require.NoError(t, suite.clientstream.Send(
			&schedulerv1pb.WatchJobsRequest{
				WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Result{
					Result: &schedulerv1pb.WatchJobsRequestResult{
						Id:     3,
						Status: schedulerv1pb.WatchJobsRequestResultStatus_FAILED,
					},
				},
			}),
		)
		require.NoError(t, suite.clientstream.Send(
			&schedulerv1pb.WatchJobsRequest{
				WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Result{
					Result: &schedulerv1pb.WatchJobsRequestResult{
						Id:     2,
						Status: schedulerv1pb.WatchJobsRequestResultStatus_FAILED,
					},
				},
			}),
		)
		require.NoError(t, suite.clientstream.Send(
			&schedulerv1pb.WatchJobsRequest{
				WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Result{
					Result: &schedulerv1pb.WatchJobsRequestResult{
						Id:     1,
						Status: schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS,
					},
				},
			}),
		)

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			ptr1 := called1.Load()
			ptr2 := called2.Load()
			ptr3 := called3.Load()
			if !assert.NotNil(c, ptr1) || !assert.NotNil(c, ptr2) || !assert.NotNil(c, ptr3) {
				return
			}
			assert.Equal(c, api.TriggerResponseResult_SUCCESS, *ptr1)
			assert.Equal(c, api.TriggerResponseResult_FAILED, *ptr2)
			assert.Equal(c, api.TriggerResponseResult_FAILED, *ptr3)
		}, time.Second*10, time.Millisecond*10)

		suite.closeserver()
		suite.streamLoop.Close(new(loops.StreamShutdown))
		suite.expectEvent(t, &loops.ConnCloseStream{StreamIDx: 123, Namespace: "ns"})
	})

	t.Run("result for unknown id leaves an existing inflight resolvable", func(t *testing.T) {
		t.Parallel()

		suite := newSuite(t)

		var called atomic.Pointer[api.TriggerResponseResult]
		suite.streamLoop.Enqueue(&loops.TriggerRequest{
			Job: &internalsv1pb.JobEvent{
				Key: "job-key-1", Name: "job-name-1",
			},
			ResultFn: func(res api.TriggerResponseResult) { called.Store(&res) },
		})

		req, err := suite.clientstream.Recv()
		require.NoError(t, err)
		exp := &schedulerv1pb.WatchJobsResponse{
			Name: "job-name-1",
			Id:   1,
		}
		assert.True(t, proto.Equal(exp, req), "%v != %v", exp, req)

		// Ack an id that was never delivered while job 1 is still inflight.
		require.NoError(t, suite.clientstream.Send(
			&schedulerv1pb.WatchJobsRequest{
				WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Result{
					Result: &schedulerv1pb.WatchJobsRequestResult{
						Id:     3,
						Status: schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS,
					},
				},
			}),
		)

		// The real inflight is untouched by the dropped result and still
		// resolves from its own ack.
		require.NoError(t, suite.clientstream.Send(
			&schedulerv1pb.WatchJobsRequest{
				WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Result{
					Result: &schedulerv1pb.WatchJobsRequestResult{
						Id:     1,
						Status: schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS,
					},
				},
			}),
		)

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			ptr := called.Load()
			if assert.NotNil(c, ptr) {
				assert.Equal(c, api.TriggerResponseResult_SUCCESS, *ptr)
			}
		}, time.Second*10, time.Millisecond*10)
		suite.expectNoEvent(t)

		suite.closeserver()
		suite.streamLoop.Close(new(loops.StreamShutdown))
		suite.expectEvent(t, &loops.ConnCloseStream{StreamIDx: 123, Namespace: "ns"})
	})

	t.Run("shutdown stream, all inflight should be marked as undeliverable", func(t *testing.T) {
		t.Parallel()

		suite := newSuite(t)

		called := make([]atomic.Pointer[api.TriggerResponseResult], 0, 10)
		for i := range 10 {
			called = append(called, atomic.Pointer[api.TriggerResponseResult]{})
			suite.streamLoop.Enqueue(&loops.TriggerRequest{
				Job: &internalsv1pb.JobEvent{
					Key: "job-key-" + strconv.Itoa(i), Name: "job-name-" + strconv.Itoa(i),
				},
				ResultFn: func(res api.TriggerResponseResult) { called[i].Store(&res) },
			})
		}

		time.Sleep(time.Second / 2)
		suite.closeserver()
		suite.streamLoop.Close(new(loops.StreamShutdown))
		suite.expectEvent(t, &loops.ConnCloseStream{StreamIDx: 123, Namespace: "ns"})
		for i := range 10 {
			ptr := called[i].Load()
			require.NotNil(t, ptr, "callback %d not called", i)
			assert.Equal(t, api.TriggerResponseResult_UNDELIVERABLE, *ptr)
		}
	})
}
