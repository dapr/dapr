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

package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"google.golang.org/protobuf/proto"

	"go.opentelemetry.io/otel/trace"

	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/backend"
)

func (o *orchestrator) callActivities(ctx context.Context, es []*backend.HistoryEvent, state *wfenginestate.State) error {
	var dueTime time.Time
	if len(state.History) > 0 {
		dueTime = state.History[0].GetTimestamp().AsTime()
	} else {
		dueTime = state.Inbox[0].GetTimestamp().AsTime()
	}

	for _, e := range es {
		err := o.callActivity(ctx, e, dueTime, state)
		if err != nil {
			if errors.Is(err, todo.ErrDuplicateInvocation) {
				log.Warnf("Workflow actor '%s': activity invocation '%s::%d' was flagged as a duplicate and will be skipped", o.actorID, e.GetTaskScheduled().GetName(), e.GetEventId())
				continue
			}

			return err
		}
	}

	return nil
}

func (o *orchestrator) callActivity(ctx context.Context, e *backend.HistoryEvent, dueTime time.Time, state *wfenginestate.State) error {
	ts := e.GetTaskScheduled()
	if ts == nil {
		log.Warnf("Workflow actor '%s': unable to process task '%v'", o.actorID, e)
		return nil
	}

	// Use stored trace context to continue the workflow trace
	if state.TraceContext != nil {
		tc := state.TraceContext
		traceID, err1 := trace.TraceIDFromHex(tc.TraceID)
		spanID, err2 := trace.SpanIDFromHex(tc.SpanID)

		if err1 == nil && err2 == nil {
			var flags trace.TraceFlags
			if _, err := fmt.Sscanf(tc.TraceFlags, "%02x", &flags); err != nil {
				log.Warnf("Workflow actor '%s': failed to parse trace flags '%s': %v", o.actorID, tc.TraceFlags, err)
			}

			var traceState trace.TraceState
			if tc.TraceState != "" {
				traceState, _ = trace.ParseTraceState(tc.TraceState)
			}

			ctx = trace.ContextWithRemoteSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
				TraceID:    traceID,
				SpanID:     spanID,
				TraceFlags: flags,
				TraceState: traceState,
				Remote:     true,
			}))
		}
	}

	eventData, err := proto.Marshal(e)
	if err != nil {
		return err
	}

	activityActorType := o.activityActorType
	if router := e.GetRouter(); router != nil && router.TargetAppID != nil {
		activityActorType = o.actorTypeBuilder.Activity(router.GetTargetAppID())
	}

	targetActorID := buildActivityActorID(o.actorID, e.GetEventId(), state.Generation)

	o.activityResultAwaited.Store(true)

	log.Debugf("Workflow actor '%s': invoking execute method on activity actor '%s||%s'", o.actorID, activityActorType, targetActorID)

	metadata := map[string][]string{
		todo.MetadataActivityReminderDueTime: {strconv.FormatInt(dueTime.UnixMilli(), 10)},
	}

	_, err = o.router.Call(ctx, internalsv1pb.
		NewInternalInvokeRequest("Execute").
		WithActor(activityActorType, targetActorID).
		WithMetadata(map[string][]string{
			todo.MetadataActivityReminderDueTime: {strconv.FormatInt(dueTime.UnixMilli(), 10)},
		}).
		WithMetadata(metadata).
		WithData(eventData).
		WithContentType(invokev1.ProtobufContentType),
	)
	if err != nil {
		return fmt.Errorf("failed to invoke activity actor '%s' to execute '%s': %w", targetActorID, ts.GetName(), err)
	}

	return nil
}

func buildActivityActorID(workflowID string, taskID int32, generation uint64) string {
	// An activity can be identified by its name followed by its task ID and generation. Example: SayHello::0::1, SayHello::1::1, etc.
	return workflowID + "::" + strconv.Itoa(int(taskID)) + "::" + strconv.FormatUint(generation, 10)
}
