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

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/golang/protobuf/ptypes/wrappers"
	"google.golang.org/grpc/codes"

	"github.com/dapr/dapr/pkg/actors"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/kit/concurrency"
)

type streamer struct {
	stream   schedulerv1pb.Scheduler_WatchJobsClient
	resultCh chan *schedulerv1pb.WatchJobsRequest

	actors   actors.ActorRuntime
	channels *channels.Channels

	wg sync.WaitGroup
}

// run starts the streamer and blocks until the stream is closed or an error occurs.
func (s *streamer) run(ctx context.Context) error {
	return concurrency.NewRunnerManager(s.receive, s.outgoing).Run(ctx)
}

// receive is a long running blocking process which listens for incoming
// scheduler job messages. It then invokes the appropriate app or actor
// reminder based on the job metadata.
func (s *streamer) receive(ctx context.Context) error {
	defer s.wg.Wait()

	for {
		resp, err := s.stream.Recv()
		if ctx.Err() != nil || errors.Is(err, io.EOF) {
			return ctx.Err()
		}
		if err != nil {
			return err
		}

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleJob(ctx, resp)
			select {
			case <-ctx.Done():
			case <-s.stream.Context().Done():
			case s.resultCh <- &schedulerv1pb.WatchJobsRequest{
				WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Result{
					Result: &schedulerv1pb.WatchJobsRequestResult{Uuid: resp.GetUuid()},
				},
			}:
			}
		}()
	}
}

// outgoing is a long running blocking process which sends ACK scheduler job
// results back to the Scheduler. Ack messages are collected via a channel to
// ensure they are sent unary over the stream- gRPC does not support parallel
// message sends.
func (s *streamer) outgoing(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.stream.Context().Done():
			return s.stream.Context().Err()
		case result := <-s.resultCh:
			if err := s.stream.Send(result); err != nil {
				return err
			}
		}
	}
}

// handleJob invokes the appropriate app or actor reminder based on the job metadata.
func (s *streamer) handleJob(ctx context.Context, job *schedulerv1pb.WatchJobsResponse) {
	meta := job.GetMetadata()

	switch t := meta.GetTarget(); t.GetType().(type) {
	case *schedulerv1pb.JobTargetMetadata_Job:
		if err := s.invokeApp(ctx, job); err != nil {
			log.Errorf("failed to invoke schedule app job: %s", err)
		}

	case *schedulerv1pb.JobTargetMetadata_Actor:
		if err := s.invokeActorReminder(ctx, job); err != nil {
			log.Errorf("failed to invoke scheduled actor reminder: %s", err)
		}

	default:
		log.Errorf("Unknown job metadata type: %+v", t)
	}
}

// invokeApp calls the local app with the given job data.
func (s *streamer) invokeApp(ctx context.Context, job *schedulerv1pb.WatchJobsResponse) error {
	appChannel := s.channels.AppChannel()
	if appChannel == nil {
		return errors.New("received job, but app channel not initialized")
	}

	req := invokev1.NewInvokeMethodRequest("job/"+job.GetName()).
		WithHTTPExtension(http.MethodPost, "").
		WithDataObject(job.GetData())
	defer req.Close()

	response, err := appChannel.TriggerJob(ctx, req)
	if err != nil {
		return err
	}

	if response != nil {
		defer response.Close()
	}
	if err != nil {
		// TODO(Cassie): add an orphaned job go routine to retry sending job at a later time
		return fmt.Errorf("error returned from app channel while sending triggered job to app: %w", err)
	}

	// TODO: standardize on the error code returned by both protocol channels,
	// converting HTTP status codes to gRPC codes
	statusCode := response.Status().GetCode()
	switch codes.Code(statusCode) {
	case codes.OK:
		log.Debugf("Sent job %s to app", job.GetName())
	case codes.NotFound:
		log.Errorf("non-retriable error returned from app while processing triggered job %v: %s. status code returned: %v", job.GetName(), statusCode)
	default:
		log.Errorf("unexpected status code returned from app while processing triggered job %s. status code returned: %v", job.GetName(), statusCode)
	}

	return nil
}

// invokeActorReminder calls the actor ID with the given reminder data.
func (s *streamer) invokeActorReminder(ctx context.Context, job *schedulerv1pb.WatchJobsResponse) error {
	if s.actors == nil {
		return errors.New("received actor reminder job but actor runtime is not initialized")
	}

	actor := job.GetMetadata().GetTarget().GetActor()

	var jspb wrappers.BytesValue
	if job.GetData() != nil {
		if err := job.GetData().UnmarshalTo(&jspb); err != nil {
			return fmt.Errorf("failed to unmarshal reminder data: %s", err)
		}
	}

	return s.actors.ExecuteLocalOrRemoteActorReminder(ctx, &actors.CreateReminderRequest{
		Name:      job.GetName(),
		ActorType: actor.GetType(),
		ActorID:   actor.GetId(),
		Data:      jspb.GetValue(),
	})
}
