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

package fork

import (
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

type Options struct {
	AppID             string
	ActorType         string
	ActivityActorType string
	InstanceID        string
	NewInstanceID     string
	TargetEventID     int32

	OverwriteInput             bool
	Input                      *wrapperspb.StringValue
	NewChildWorkflowInstanceID *string

	OldState *state.State
}

type Fork struct {
	instanceID    string
	newInstanceID string

	oldState      *state.State
	targetEventID int32
	newState      *state.State

	overwriteInput             bool
	input                      *wrapperspb.StringValue
	newChildWorkflowInstanceID *string

	unfinishedActivities     map[int32]*backend.HistoryEvent
	activeTimers             map[int32]*backend.HistoryEvent
	unfinishedChildWorkflows map[int32]*backend.HistoryEvent
}

func New(opts Options) *Fork {
	return &Fork{
		instanceID:    opts.InstanceID,
		newInstanceID: opts.NewInstanceID,
		oldState:      opts.OldState,
		targetEventID: opts.TargetEventID,
		newState: state.NewState(state.Options{
			AppID:             opts.AppID,
			WorkflowActorType: opts.ActorType,
			ActivityActorType: opts.ActivityActorType,
		}),
		overwriteInput:             opts.OverwriteInput,
		input:                      opts.Input,
		newChildWorkflowInstanceID: opts.NewChildWorkflowInstanceID,
		unfinishedActivities:       make(map[int32]*backend.HistoryEvent),
		activeTimers:               make(map[int32]*backend.HistoryEvent),
		unfinishedChildWorkflows:   make(map[int32]*backend.HistoryEvent),
	}
}

func (f *Fork) Build() (*state.State, error) {
	var found *protos.HistoryEvent
	for i, his := range f.oldState.History {
		if his.GetEventId() != f.targetEventID {
			f.handleBefore(his)
			continue
		}

		var err error
		found, err = f.handleFound(i, his)
		if err != nil {
			return nil, err
		}

		break
	}

	if found == nil {
		return nil, status.Errorf(codes.NotFound, "does not have history event with ID '%d'", f.targetEventID)
	}

	// TODO: @joshvanl: We should be able to rerun workflows even if there are
	// active timers. Timers should be re-created with the same due-time as the
	// original workflow instance. To do this, we need to create a new timer
	// actor. Support rerunning active timers in a future change.

	// Ensure incomplete activities are also rerun.
	for _, unfin := range f.unfinishedActivities {
		f.newState.AddToInbox(unfin)
	}

	// Ensure incomplete timers are also rerun.
	for _, unfin := range f.activeTimers {
		f.newState.AddToInbox(unfin)
	}

	// Ensure incomplete child workflows are also rerun.
	for _, unfin := range f.unfinishedChildWorkflows {
		sub := unfin.GetSubOrchestrationInstanceCreated()
		sub.InstanceId = fmt.Sprintf("%s:%04x", f.newInstanceID, unfin.EventId)
		f.newState.AddToInbox(unfin)
	}

	f.newState.AddToInbox(found)

	return f.newState, nil
}

func (f *Fork) handleBefore(his *backend.HistoryEvent) {
	// Track activities which have not been completed yet so they are also
	// rerun.
	switch his.GetEventType().(type) {
	case *protos.HistoryEvent_TaskScheduled:
		f.unfinishedActivities[his.GetEventId()] = his

	case *protos.HistoryEvent_TaskCompleted:
		f.newState.AddToHistory(f.unfinishedActivities[his.GetTaskCompleted().GetTaskScheduledId()])
		f.newState.AddToHistory(his)
		delete(f.unfinishedActivities, his.GetTaskCompleted().GetTaskScheduledId())

	case *protos.HistoryEvent_TaskFailed:
		f.newState.AddToHistory(f.unfinishedActivities[his.GetTaskFailed().GetTaskScheduledId()])
		f.newState.AddToHistory(his)
		delete(f.unfinishedActivities, his.GetTaskFailed().GetTaskScheduledId())

	case *protos.HistoryEvent_TimerCreated:
		f.activeTimers[his.GetEventId()] = his

	case *protos.HistoryEvent_SubOrchestrationInstanceCreated:
		f.unfinishedChildWorkflows[his.GetEventId()] = his

	case *protos.HistoryEvent_SubOrchestrationInstanceCompleted:
		f.newState.AddToHistory(f.unfinishedChildWorkflows[his.GetSubOrchestrationInstanceCompleted().GetTaskScheduledId()])
		f.newState.AddToHistory(his)
		delete(f.unfinishedChildWorkflows, his.GetSubOrchestrationInstanceCompleted().GetTaskScheduledId())

	case *protos.HistoryEvent_SubOrchestrationInstanceFailed:
		f.newState.AddToHistory(f.unfinishedChildWorkflows[his.GetSubOrchestrationInstanceFailed().GetTaskScheduledId()])
		f.newState.AddToHistory(his)
		delete(f.unfinishedChildWorkflows, his.GetSubOrchestrationInstanceFailed().GetTaskScheduledId())

	default:
		f.newState.AddToHistory(his)
	}
}

func (f *Fork) handleFound(i int, his *backend.HistoryEvent) (*protos.HistoryEvent, error) {
	switch his.GetEventType().(type) {
	case *protos.HistoryEvent_TaskScheduled:
		sched := his.GetTaskScheduled()
		sched.RerunParentInstanceInfo = &protos.RerunParentInstanceInfo{
			InstanceID: f.instanceID,
		}
		if f.overwriteInput {
			sched.Input = f.input
		}
		if f.newChildWorkflowInstanceID != nil {
			return nil, status.Errorf(codes.InvalidArgument, "cannot set new child workflow instance ID on activity event '%d'", f.targetEventID)
		}
		return his, nil

	case *protos.HistoryEvent_TimerCreated:
		timer := his.GetTimerCreated()
		timer.RerunParentInstanceInfo = &protos.RerunParentInstanceInfo{
			InstanceID: f.instanceID,
		}
		if f.overwriteInput {
			return nil, status.Errorf(codes.InvalidArgument, "cannot write input to timer event '%d'", f.targetEventID)
		}
		if f.newChildWorkflowInstanceID != nil {
			return nil, status.Errorf(codes.InvalidArgument, "cannot set new child workflow instance ID on timer event '%d'", f.targetEventID)
		}
		return his, nil

	case *protos.HistoryEvent_SubOrchestrationInstanceCreated:
		sub := his.GetSubOrchestrationInstanceCreated()
		sub.RerunParentInstanceInfo = &protos.RerunParentInstanceInfo{
			InstanceID: f.instanceID,
		}

		if f.newChildWorkflowInstanceID != nil {
			sub.InstanceId = *f.newChildWorkflowInstanceID
		} else {
			sub.InstanceId = fmt.Sprintf("%s:%04x", f.newInstanceID, his.EventId)
		}

		if f.overwriteInput {
			sub.Input = f.input
		}

		return his, nil

	default:
		return nil, status.Errorf(codes.NotFound, "target event '%T' with ID '%d' is not an event that can be rerun", his.GetEventType(), f.targetEventID)
	}
}
