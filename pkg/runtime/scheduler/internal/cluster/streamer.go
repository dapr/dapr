/*
Copyright 2024 The Dapr Authors
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
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/actors/api"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	"github.com/dapr/dapr/pkg/actors/router"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/wfengine"
	"github.com/dapr/kit/concurrency"
)

type streamer struct {
	stream   schedulerv1pb.Scheduler_WatchJobsClient
	resultCh chan *schedulerv1pb.WatchJobsRequest

	actors   router.Interface
	channels *channels.Channels
	wfengine wfengine.Interface

	wg       sync.WaitGroup
	inflight atomic.Int64
}

// run starts the streamer and blocks until the stream is closed or an error occurs.
func (s *streamer) run(ctx context.Context) error {
	return concurrency.NewRunnerManager(s.receive, s.outgoing).Run(ctx)
}

// receive is a long running blocking process which listens for incoming
// scheduler job messages. It then invokes the appropriate app or actor
// reminder based on the job metadata.
func (s *streamer) receive(ctx context.Context) error {
	defer func() {
		s.wg.Wait()
		s.stream.CloseSend()
	}()

	for {
		resp, err := s.stream.Recv()
		if ctx.Err() != nil || errors.Is(err, io.EOF) {
			return ctx.Err()
		}
		if err != nil {
			return err
		}

		s.wg.Add(1)
		s.inflight.Add(1)
		go func() {
			defer func() {
				s.wg.Done()
				s.inflight.Add(-1)
			}()

			result := s.handleJob(ctx, resp)
			select {
			case s.resultCh <- &schedulerv1pb.WatchJobsRequest{
				WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Result{
					Result: &schedulerv1pb.WatchJobsRequestResult{
						Id:     resp.GetId(),
						Status: result,
					},
				},
			}:
			case <-s.stream.Context().Done():
			case <-ctx.Done():
			}
		}()
	}
}

// outgoing is a long running blocking process which sends ACK scheduler job
// results back to the Scheduler. Ack messages are collected via a channel to
// ensure they are sent unary over the stream- gRPC does not support parallel
// message sends.
func (s *streamer) outgoing(ctx context.Context) error {
	defer s.stream.CloseSend()

	for {
		select {
		case result := <-s.resultCh:
			if err := s.stream.Send(result); err != nil {
				return err
			}
		case <-ctx.Done():
			if s.inflight.Load() == 0 && len(s.resultCh) == 0 {
				return ctx.Err()
			}
		}
	}
}

// handleJob invokes the appropriate app or actor reminder based on the job metadata.
func (s *streamer) handleJob(ctx context.Context, job *schedulerv1pb.WatchJobsResponse) schedulerv1pb.WatchJobsRequestResultStatus {
	meta := job.GetMetadata()

	switch t := meta.GetTarget(); t.GetType().(type) {
	case *schedulerv1pb.JobTargetMetadata_Job:
		if err := s.invokeApp(ctx, job); err != nil {
			log.Errorf("failed to invoke schedule app job: %s", err)
			return schedulerv1pb.WatchJobsRequestResultStatus_FAILED
		}
		return schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS

	case *schedulerv1pb.JobTargetMetadata_Actor:
		err := s.invokeActorReminder(ctx, job)
		if err == nil {
			return schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS
		}

		if errors.Is(err, context.Canceled) {
			return schedulerv1pb.WatchJobsRequestResultStatus_FAILED
		}

		if errors.Is(err, actorerrors.ErrReminderCanceled) {
			// TODO: @joshvanl add support for cancelling a job with a new
			// WatchJobsRequestResultStatus_CANCELED.
			return schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS
		}

		// If the actor was hosted on another instance, the error will be a gRPC status error,
		// so we need to unwrap it and match on the error message
		if st, ok := status.FromError(err); ok {
			if st.Code() == codes.FailedPrecondition {
				return schedulerv1pb.WatchJobsRequestResultStatus_FAILED
			}
			if st.Message() == actorerrors.ErrReminderCanceled.Error() {
				return schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS
			}
		}

		log.Errorf("failed to invoke scheduled actor reminder named: %s due to: %s", job.GetName(), err)
		return schedulerv1pb.WatchJobsRequestResultStatus_FAILED

	default:
		log.Errorf("Unknown job metadata type: %+v", t)
		return schedulerv1pb.WatchJobsRequestResultStatus_FAILED
	}
}

// invokeApp calls the local app with the given job data.
func (s *streamer) invokeApp(ctx context.Context, job *schedulerv1pb.WatchJobsResponse) error {
	appChannel := s.channels.AppChannel()
	if appChannel == nil {
		return errors.New("received job, but app channel not initialized")
	}

	response, err := appChannel.TriggerJob(ctx, job.GetName(), job.GetData())
	if err != nil {
		return fmt.Errorf("error returned from app channel while sending triggered job to app: %w", err)
	}
	if response != nil {
		defer response.Close()
	}

	// TODO: standardize on the error code returned by both protocol channels,
	// converting HTTP status codes to gRPC codes
	statusCode := response.Status().GetCode()
	// TODO: fix types
	//nolint:gosec
	switch codes.Code(statusCode) {
	case codes.OK:
		log.Debugf("Sent job %s to app", job.GetName())
		return nil
	case codes.NotFound:
		log.Errorf("non-retriable error returned from app while processing triggered job %s. status code returned: %v", job.GetName(), statusCode)
		// return nil to signal SUCCESS
		return nil
	default:
		err := fmt.Errorf("unexpected status code returned from app while processing triggered job %s. status code returned: %v", job.GetName(), statusCode)
		log.Error(err.Error())
		return err
	}
}

// invokeActorReminder calls the actor ID with the given reminder data.
func (s *streamer) invokeActorReminder(ctx context.Context, job *schedulerv1pb.WatchJobsResponse) error {
	if s.actors == nil {
		return errors.New("received actor reminder job but actor runtime is not initialized")
	}

	actor := job.GetMetadata().GetTarget().GetActor()

	err := s.actors.CallReminder(ctx, &api.Reminder{
		Name:      job.GetName(),
		ActorType: actor.GetType(),
		ActorID:   actor.GetId(),
		Data:      job.GetData(),
		SkipLock:  actor.GetType() == s.wfengine.ActivityActorType(),
	})
	diag.DefaultMonitoring.ActorReminderFired(actor.GetType(), err == nil)
	if err != nil {
		return err
	}
	return nil
}
