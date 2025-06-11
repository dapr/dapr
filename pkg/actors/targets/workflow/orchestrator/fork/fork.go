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
	TargetEventID     int32

	OverwriteInput bool
	Input          *wrapperspb.StringValue

	OldState *state.State
}

type Fork struct {
	oldState      *state.State
	targetEventID int32
	newState      *state.State

	overwriteInput bool
	input          *wrapperspb.StringValue

	unfinishedActivities    map[int32]*backend.HistoryEvent
	activeTimers            map[int32]*backend.HistoryEvent
	activeSubOrchestrations map[int32]*backend.HistoryEvent
}

func New(opts Options) *Fork {
	return &Fork{
		oldState:      opts.OldState,
		targetEventID: opts.TargetEventID,
		newState: state.NewState(state.Options{
			AppID:             opts.AppID,
			WorkflowActorType: opts.ActorType,
			ActivityActorType: opts.ActivityActorType,
		}),
		overwriteInput:          opts.OverwriteInput,
		input:                   opts.Input,
		unfinishedActivities:    make(map[int32]*backend.HistoryEvent),
		activeTimers:            make(map[int32]*backend.HistoryEvent),
		activeSubOrchestrations: make(map[int32]*backend.HistoryEvent),
	}
}

func (f *Fork) Build() (*state.State, error) {
	var found bool
	for i, his := range f.oldState.History {
		if his.GetEventId() != f.targetEventID {
			f.handleBefore(his)
			continue
		}

		if err := f.handleFound(i, his); err != nil {
			return nil, err
		}

		found = true
		break
	}

	if !found {
		return nil, status.Errorf(codes.NotFound, "does not have history event with ID '%d'", f.targetEventID)
	}

	// TODO: @joshvanl: We should be able to rerun workflows even if there are
	// active timers. Timers should be re-created with the same due-time as the
	// original workflow instance.
	// To do this, we need to create a new timer actor.
	// Support rerunning active timers in a future change.

	// Ensure incomplete activities are also rerun.
	for _, unfin := range f.unfinishedActivities {
		f.newState.AddToInbox(unfin)
	}

	// Ensure incomplete timers are also rerun.
	for _, unfin := range f.activeTimers {
		f.newState.AddToInbox(unfin)
	}

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

	case *protos.HistoryEvent_TimerCreated:
		f.activeTimers[his.GetEventId()] = his

	case *protos.HistoryEvent_TimerFired:
		f.newState.AddToHistory(f.activeTimers[his.GetTimerFired().GetTimerId()])
		f.newState.AddToHistory(his)
		delete(f.activeTimers, his.GetTimerFired().GetTimerId())

	default:
		f.newState.AddToHistory(his)
	}
}

func (f *Fork) handleFound(i int, his *backend.HistoryEvent) error {
	switch his.GetEventType().(type) {
	case *protos.HistoryEvent_TaskScheduled:
		if f.overwriteInput {
			sched := his.GetTaskScheduled()
			sched.Input = f.input
		}
		f.newState.AddToInbox(his)
		return nil

	case *protos.HistoryEvent_TimerCreated:
		if f.overwriteInput {
			return status.Errorf(codes.InvalidArgument, "cannot write input to timer event '%d'", f.targetEventID)
		}
		f.newState.AddToInbox(his)
		return nil

	default:
		return status.Errorf(codes.NotFound, "target event '%T' with ID '%d' is not an event that can be rerun", his.GetEventType(), f.targetEventID)
	}
}
