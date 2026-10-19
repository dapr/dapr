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
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	wfenginefake "github.com/dapr/dapr/pkg/runtime/wfengine/fake"
	testinggrpc "github.com/dapr/dapr/pkg/testing/grpc"
	"github.com/dapr/kit/logger"
)

// grpcLikeStream mirrors grpc-go's unsynchronized sentLast in Send/CloseSend.
type grpcLikeStream struct {
	ctx    context.Context
	recvFn func(ctx context.Context) (*schedulerv1pb.WatchJobsResponse, error)

	sentLast bool

	closeSendCalls atomic.Int32
	sends          atomic.Int32
	sendAfterClose atomic.Int32
}

func (s *grpcLikeStream) Send(*schedulerv1pb.WatchJobsRequest) error {
	if s.sentLast {
		s.sendAfterClose.Add(1)
		return errors.New("SendMsg called after CloseSend")
	}
	s.sends.Add(1)
	return nil
}

func (s *grpcLikeStream) CloseSend() error {
	s.closeSendCalls.Add(1)
	if s.sentLast {
		return nil
	}
	s.sentLast = true
	return nil
}

func (s *grpcLikeStream) Recv() (*schedulerv1pb.WatchJobsResponse, error) {
	return s.recvFn(s.ctx)
}

func (s *grpcLikeStream) Header() (metadata.MD, error) { return nil, nil }
func (s *grpcLikeStream) Trailer() metadata.MD         { return nil }
func (s *grpcLikeStream) Context() context.Context     { return s.ctx }
func (s *grpcLikeStream) SendMsg(any) error            { return nil }
func (s *grpcLikeStream) RecvMsg(any) error            { return nil }

func actorJob(id int) *schedulerv1pb.WatchJobsResponse {
	return &schedulerv1pb.WatchJobsResponse{
		Id:   uint64(id), //nolint:gosec
		Name: "job-" + strconv.Itoa(id),
		Metadata: &schedulerv1pb.JobMetadata{
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Actor{
					Actor: &schedulerv1pb.TargetActorReminder{
						Type: "myactortype",
						Id:   "id-" + strconv.Itoa(id),
					},
				},
			},
		},
	}
}

func newTestStreamer(stream schedulerv1pb.Scheduler_WatchJobsClient, reminderFn func(context.Context, *actorapi.Reminder) error) *streamer {
	return &streamer{
		stream:   stream,
		resultCh: make(chan *schedulerv1pb.WatchJobsRequest),
		actors:   routerfake.New().WithCallReminderFn(reminderFn),
		wfengine: wfenginefake.New(),
	}
}

func Test_streamer_parentCancel_singleCloseSend(t *testing.T) {
	t.Parallel()

	for i := range 200 {
		ctx, cancel := context.WithCancel(t.Context())
		stream := &grpcLikeStream{
			ctx: ctx,
			recvFn: func(ctx context.Context) (*schedulerv1pb.WatchJobsResponse, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
		}

		s := newTestStreamer(stream, func(context.Context, *actorapi.Reminder) error { return nil })

		errCh := make(chan error, 1)
		go func() { errCh <- s.run(ctx) }()

		cancel()

		select {
		case err := <-errCh:
			require.NoError(t, err, "iteration %d", i)
		case <-time.After(5 * time.Second):
			require.FailNow(t, "streamer did not return after cancel", "iteration %d", i)
		}

		require.Equal(t, int32(1), stream.closeSendCalls.Load(),
			"iteration %d: CloseSend must be called exactly once", i)
	}
}

func Test_streamer_recvError_closeSendAfterLastSend(t *testing.T) {
	t.Parallel()

	const jobs = 32

	for i := range 50 {
		ctx, cancel := context.WithCancel(t.Context())

		var next atomic.Int32
		stream := &grpcLikeStream{
			ctx: ctx,
			recvFn: func(context.Context) (*schedulerv1pb.WatchJobsResponse, error) {
				n := int(next.Add(1))
				if n <= jobs {
					return actorJob(n), nil
				}
				return nil, io.ErrUnexpectedEOF
			},
		}

		s := newTestStreamer(stream, func(context.Context, *actorapi.Reminder) error { return nil })

		errCh := make(chan error, 1)
		go func() { errCh <- s.run(ctx) }()

		select {
		case err := <-errCh:
			require.ErrorIs(t, err, io.ErrUnexpectedEOF, "iteration %d", i)
		case <-time.After(5 * time.Second):
			require.FailNow(t, "streamer did not return after Recv error", "iteration %d", i)
		}
		cancel()

		assert.Equal(t, int32(jobs), stream.sends.Load(),
			"iteration %d: every job result must be sent before CloseSend", i)
		assert.Equal(t, int32(0), stream.sendAfterClose.Load(),
			"iteration %d: no Send may follow CloseSend", i)
		assert.Equal(t, int32(1), stream.closeSendCalls.Load(),
			"iteration %d: CloseSend must be called exactly once", i)
	}
}

func Test_streamer_parentCancel_withInflightJobs(t *testing.T) {
	t.Parallel()

	const jobs = 16

	for i := range 50 {
		ctx, cancel := context.WithCancel(t.Context())

		var next atomic.Int32
		stream := &grpcLikeStream{
			ctx: ctx,
			recvFn: func(ctx context.Context) (*schedulerv1pb.WatchJobsResponse, error) {
				n := int(next.Add(1))
				if n <= jobs {
					return actorJob(n), nil
				}
				<-ctx.Done()
				return nil, ctx.Err()
			},
		}

		var started sync.WaitGroup
		started.Add(jobs)
		s := newTestStreamer(stream, func(context.Context, *actorapi.Reminder) error {
			started.Done()
			return nil
		})

		errCh := make(chan error, 1)
		go func() { errCh <- s.run(ctx) }()

		started.Wait()
		cancel()

		select {
		case err := <-errCh:
			require.NoError(t, err, "iteration %d", i)
		case <-time.After(5 * time.Second):
			require.FailNow(t, "streamer did not return after cancel", "iteration %d", i)
		}

		assert.Equal(t, int32(0), stream.sendAfterClose.Load(),
			"iteration %d: no Send may follow CloseSend", i)
		assert.Equal(t, int32(1), stream.closeSendCalls.Load(),
			"iteration %d: CloseSend must be called exactly once", i)
	}
}

type watchJobsServer struct {
	schedulerv1pb.UnimplementedSchedulerServer
	jobs    int
	results chan uint64
}

func (w *watchJobsServer) WatchJobs(stream schedulerv1pb.Scheduler_WatchJobsServer) error {
	for i := 1; i <= w.jobs; i++ {
		if err := stream.Send(actorJob(i)); err != nil {
			return err
		}
	}

	for {
		req, err := stream.Recv()
		if err != nil {
			return err
		}
		select {
		case w.results <- req.GetResult().GetId():
		case <-stream.Context().Done():
			return nil
		}
	}
}

func Test_streamer_parentCancel_bufconn(t *testing.T) {
	t.Parallel()

	const jobs = 8

	for i := range 25 {
		srv := &watchJobsServer{jobs: jobs, results: make(chan uint64, jobs)}
		client, cleanup, err := testinggrpc.TestServerFor(
			logger.NewLogger("test"),
			func(s *grpc.Server, srv schedulerv1pb.SchedulerServer) {
				schedulerv1pb.RegisterSchedulerServer(s, srv)
			},
			schedulerv1pb.NewSchedulerClient,
		)(srv)
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())
		stream, err := client.WatchJobs(ctx)
		require.NoError(t, err)

		s := newTestStreamer(stream, func(context.Context, *actorapi.Reminder) error { return nil })

		errCh := make(chan error, 1)
		go func() { errCh <- s.run(ctx) }()

		for range jobs {
			select {
			case <-srv.results:
			case <-time.After(5 * time.Second):
				require.FailNow(t, "server did not receive all job results", "iteration %d", i)
			}
		}

		cancel()

		select {
		case err := <-errCh:
			// grpc-go can see the parent cancel before the streamer's derived ctx.
			if err != nil {
				require.Equal(t, codes.Canceled, status.Code(err), "iteration %d: %v", i, err)
			}
		case <-time.After(5 * time.Second):
			require.FailNow(t, "streamer did not return after cancel", "iteration %d", i)
		}

		cleanup()
	}
}
