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
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workflowacl "github.com/dapr/dapr/pkg/acl/workflow"
	"github.com/dapr/dapr/pkg/actors/api"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	"github.com/dapr/dapr/pkg/actors/router"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/activity"
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

	// addr is the address of the scheduler this stream is connected to.
	addr string
	// pull is set when this sidecar pull-dispatches activities of
	// pullActorType and every scheduler supports it. A pull-flagged job then
	// executes here, and only then: the scheduler falls back to regular
	// routing when no sidecar offers slots, and that delivery must forward to
	// the placement owner like any other reminder.
	pull          *Pull
	pullActorType string

	// pullCh carries pull messages to outgoing, which reports whether Send
	// succeeded. done is closed when outgoing stops sending.
	pullCh chan pullSend
	done   chan struct{}

	wg       sync.WaitGroup
	inflight atomic.Int64
}

// run starts the streamer and blocks until the stream is closed or an error occurs.
func (s *streamer) run(ctx context.Context) error {
	if s.done == nil {
		s.done = make(chan struct{})
	}
	if s.pullCh == nil {
		s.pullCh = make(chan pullSend)
	}
	runners := []concurrency.Runner{s.receive, s.outgoing}
	if s.pull != nil {
		s.pull.attach(s)
		defer s.pull.detach(s)
		runners = append(runners, s.reportPull)
	}
	return concurrency.NewRunnerManager(runners...).Run(ctx)
}

type pullSend struct {
	req  *schedulerv1pb.WatchJobsRequest
	sent chan bool
}

// send sends msg on the stream, returning whether it was sent.
func (s *streamer) send(msg *schedulerv1pb.WatchJobsRequest) bool {
	sent := make(chan bool, 1)
	select {
	case s.pullCh <- pullSend{req: msg, sent: sent}:
		return <-sent
	case <-s.done:
		return false
	}
}

// isPull reports whether job is a pull delivery this sidecar executes itself.
func (s *streamer) isPull(job *schedulerv1pb.WatchJobsResponse) bool {
	return s.pull != nil && job.GetPull() &&
		job.GetMetadata().GetTarget().GetActor().GetType() == s.pullActorType
}

// pullReportInterval is how often the reporting stream repeats the free slots
// when nothing changed. The scheduler overwrites its count with a stream's
// initial request and zeroes it on a refusal, and neither is ordered with the
// reports of another stream, so a stale value can land last. The repeat
// bounds how long work then stays parked.
const pullReportInterval = 5 * time.Second

// reportPull sends the results of pull reminders that finished while no
// stream to this scheduler was connected, then reports free slots on every
// change, and every pullReportInterval, while this stream is the scheduler's
// reporting stream. Every change is sent even when the count looks the same:
// the scheduler takes a slot off its count for each delivery, and an activity
// that starts and finishes between two reports must still give it back.
func (s *streamer) reportPull(ctx context.Context) error {
	if !s.pull.flush(s) {
		return nil
	}

	for {
		free, freeChanged := s.wfengine.ActivityFreeSlots()
		reporter, reporterChanged := s.pull.reporter(s)
		if reporter {
			if !s.send(&schedulerv1pb.WatchJobsRequest{
				WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_PullSlots{
					PullSlots: &schedulerv1pb.PullSlots{ActorType: s.pullActorType, Free: free},
				},
			}) {
				return nil
			}
		}

		select {
		case <-freeChanged:
		case <-reporterChanged:
		case <-time.After(pullReportInterval):
		case <-s.done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// receive is a long running blocking process which listens for incoming
// scheduler job messages. It then invokes the appropriate app or actor
// reminder based on the job metadata.
// It must not call CloseSend; outgoing owns the send side.
func (s *streamer) receive(ctx context.Context) error {
	// Jobs also stop when the stream ends. A job can wait on something only a
	// reconnected stream provides, such as actor placement when the scheduler
	// hosts it, and waiting for that job here would stop the reconnect.
	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(s.stream.Context(), cancel)
	defer stop()
	defer s.wg.Wait()

	for {
		resp, err := s.stream.Recv()
		if ctx.Err() != nil || errors.Is(err, io.EOF) {
			return ctx.Err()
		}

		if err != nil {
			return err
		}

		// A pull reminder outlives this stream: it keeps running and its
		// result goes back on whichever stream to this scheduler is live.
		if s.isPull(resp) {
			s.pull.run(s, resp.GetId(), resp.GetMetadata().GetTarget().GetActor(), resp.GetName(), func(ctx context.Context) schedulerv1pb.WatchJobsRequestResultStatus {
				return s.handleJob(ctx, resp)
			})
			continue
		}

		s.wg.Add(1)
		s.inflight.Add(1)

		go func() {
			defer func() {
				s.wg.Done()
				s.inflight.Add(-1)
			}()

			result := s.handleJob(jobCtx, resp)
			select {
			case s.resultCh <- &schedulerv1pb.WatchJobsRequest{
				WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Result{
					Result: &schedulerv1pb.WatchJobsRequestResult{
						Id:     resp.GetId(),
						Status: result,
					},
				},
			}:
			case <-jobCtx.Done():
			}
		}()
	}
}

// outgoing is a long running blocking process which sends ACK scheduler job
// results back to the Scheduler. Ack messages are collected via a channel to
// ensure they are sent unary over the stream- gRPC does not support parallel
// message sends.
// It is the only caller of Send and CloseSend.
func (s *streamer) outgoing(ctx context.Context) error {
	defer close(s.done)
	defer s.stream.CloseSend()

	for {
		select {
		case result := <-s.resultCh:
			err := s.stream.Send(result)
			if err != nil {
				return err
			}
		case m := <-s.pullCh:
			err := s.stream.Send(m.req)
			m.sent <- err == nil
			if err != nil {
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

	switch t := meta.GetTarget().GetType().(type) {
	case *schedulerv1pb.JobTargetMetadata_Job:
		err := s.invokeApp(ctx, job)
		if err != nil {
			log.Errorf("failed to invoke schedule app job: %s", err)
			return schedulerv1pb.WatchJobsRequestResultStatus_FAILED
		}

		return schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS

	case *schedulerv1pb.JobTargetMetadata_Actor:
		// The generated getters are nil-safe on messages but not on the oneof
		// wrapper: a typed-nil wrapper or a missing actor payload would
		// nil-dereference below. Reject the job instead of panicking. FAILED
		// is the only rejection the protocol offers, so a malformed job is
		// left to its failure policy rather than crashing the runtime.
		if t == nil || t.Actor == nil || t.Actor.GetType() == "" || t.Actor.GetId() == "" {
			log.Errorf("received scheduled job '%s' with invalid actor target metadata, rejecting", job.GetName())
			return schedulerv1pb.WatchJobsRequestResultStatus_FAILED
		}

		actorType := t.Actor.GetType()

		err := s.invokeActorReminder(ctx, job, t.Actor)
		if err == nil {
			return schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS
		}

		if errors.Is(err, activity.ErrNoCapacity) {
			return schedulerv1pb.WatchJobsRequestResultStatus_NO_CAPACITY
		}
		// A pull delivery to a host that no longer hosts the actor type, such
		// as one whose worker just left: the scheduler picks another sidecar.
		if s.isPull(job) && errors.Is(err, actorerrors.ErrCreatingActor) {
			return schedulerv1pb.WatchJobsRequestResultStatus_NO_CAPACITY
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
		// so we need to unwrap it and match on the error message.
		if st, ok := status.FromError(err); ok {
			if st.Code() == codes.FailedPrecondition {
				return schedulerv1pb.WatchJobsRequestResultStatus_FAILED
			}

			// Recognise the user-app "cancel this reminder" signal that crossed a
			// daprd-to-daprd boundary: app.go raises ErrReminderCanceled when the
			// app responded with the X-Daprremindercancel header, and gRPC strips
			// the sentinel wrapping so we only see the message text on this side.
			// Map it to SUCCESS so the scheduler deletes the recurring job rather
			// than retrying forever.
			_, isInternalActor := workflowacl.ParseActorType(actorType)
			if !isInternalActor && st.Message() == actorerrors.ErrReminderCanceled.Error() {
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

	start := time.Now()

	response, err := appChannel.TriggerJob(ctx, job.GetName(), job.GetData())
	if err != nil {
		return fmt.Errorf("error returned from app channel while sending triggered job to app: %w", err)
	}

	if response != nil {
		defer response.Close()
	}

	elapsedMs := diag.ElapsedSince(start)
	// TODO: standardize on the error code returned by both protocol channels,
	// converting HTTP status codes to gRPC codes
	statusCode := response.Status().GetCode()
	// TODO: fix types
	//nolint:gosec
	switch codes.Code(statusCode) {
	case codes.OK:
		log.Debugf("Sent job %s to app", job.GetName())
		diag.DefaultComponentMonitoring.JobTriggeredSuccess(ctx, diag.JobTriggerOp, elapsedMs)

		return nil
	case codes.NotFound:
		// NotFound treated as SUCCESS to avoid retriggers by the scheduler, but is monitored as a failure
		log.Errorf("non-retriable error returned from app while processing triggered job %s. status code returned: %v", job.GetName(), statusCode)
		diag.DefaultComponentMonitoring.JobTriggeredFailure(ctx, diag.JobTriggerOp, elapsedMs)
		// return nil to signal SUCCESS
		return nil
	default:
		err := fmt.Errorf("unexpected status code returned from app while processing triggered job %s. status code returned: %v", job.GetName(), statusCode)
		log.Error(err.Error())
		diag.DefaultComponentMonitoring.JobTriggeredFailure(ctx, diag.JobTriggerOp, elapsedMs)

		return err
	}
}

// invokeActorReminder calls the actor ID with the given reminder data. The
// actor target metadata has already been validated by handleJob.
func (s *streamer) invokeActorReminder(ctx context.Context, job *schedulerv1pb.WatchJobsResponse, actor *schedulerv1pb.TargetActorReminder) error {
	if s.actors == nil {
		return errors.New("received actor reminder job but actor runtime is not initialized")
	}

	err := s.actors.CallReminder(ctx, &api.Reminder{
		Name:      job.GetName(),
		ActorType: actor.GetType(),
		ActorID:   actor.GetId(),
		Data:      job.GetData(),
		SkipLock:  actor.GetType() == s.wfengine.ActivityActorType(),
		// Verified by the scheduler against the creator's SPIFFE ID at
		// schedule time and echoed back on trigger.
		SourceAppID:    job.GetMetadata().GetAppId(),
		ExecuteLocally: s.isPull(job),
	})
	diag.DefaultMonitoring.ActorReminderFired(actor.GetType(), err == nil)

	if err != nil {
		return err
	}

	return nil
}
