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

package workflow

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dapr/dapr/pkg/actors"
	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/reminders"
	"github.com/dapr/dapr/pkg/actors/router"
	"github.com/dapr/dapr/pkg/actors/state"
	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/dapr/pkg/actors/targets"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/kit/events/broadcaster"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.targets.workflow")

type EventSink func(*backend.OrchestrationMetadata)

type workflow struct {
	appID             string
	actorID           string
	actorType         string
	activityActorType string

	resiliency resiliency.Provider
	router     router.Interface
	table      table.Interface
	reminders  reminders.Interface
	actorState state.Interface

	reminderInterval time.Duration

	state            *wfenginestate.State
	rstate           *backend.OrchestrationRuntimeState
	ometa            *backend.OrchestrationMetadata
	ometaBroadcaster *broadcaster.Broadcaster[*backend.OrchestrationMetadata]

	scheduler             todo.WorkflowScheduler
	activityResultAwaited atomic.Bool
	completed             atomic.Bool
	schedulerReminders    bool
	lock                  sync.Mutex
	closeCh               chan struct{}
	closed                atomic.Bool
}

type WorkflowOptions struct {
	AppID             string
	WorkflowActorType string
	ActivityActorType string
	ReminderInterval  *time.Duration

	Resiliency         resiliency.Provider
	Actors             actors.Interface
	Scheduler          todo.WorkflowScheduler
	SchedulerReminders bool
	EventSink          EventSink
}

func WorkflowFactory(ctx context.Context, opts WorkflowOptions) (targets.Factory, error) {
	table, err := opts.Actors.Table(ctx)
	if err != nil {
		return nil, err
	}

	astate, err := opts.Actors.State(ctx)
	if err != nil {
		return nil, err
	}

	router, err := opts.Actors.Router(ctx)
	if err != nil {
		return nil, err
	}

	reminders, err := opts.Actors.Reminders(ctx)
	if err != nil {
		return nil, err
	}

	return func(actorID string) targets.Interface {
		reminderInterval := time.Minute * 1

		if opts.ReminderInterval != nil {
			reminderInterval = *opts.ReminderInterval
		}

		w := &workflow{
			appID:              opts.AppID,
			actorID:            actorID,
			actorType:          opts.WorkflowActorType,
			activityActorType:  opts.ActivityActorType,
			scheduler:          opts.Scheduler,
			reminderInterval:   reminderInterval,
			resiliency:         opts.Resiliency,
			table:              table,
			reminders:          reminders,
			router:             router,
			actorState:         astate,
			schedulerReminders: opts.SchedulerReminders,
			ometaBroadcaster:   broadcaster.New[*backend.OrchestrationMetadata](),
			closeCh:            make(chan struct{}),
		}
		if opts.EventSink != nil {
			ch := make(chan *backend.OrchestrationMetadata)
			go w.runEventSink(ch, opts.EventSink)
			// We use a Background context since this subscription should be maintained for the entire lifecycle of this workflow actor. The subscription will be shutdown during the actor deactivation.
			w.ometaBroadcaster.Subscribe(context.Background(), ch)
		}
		return w
	}, nil
}

// InvokeMethod implements actors.InternalActor
func (w *workflow) InvokeMethod(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
	return w.handleInvoke(ctx, req)
}

// InvokeReminder implements actors.InternalActor
func (w *workflow) InvokeReminder(ctx context.Context, reminder *actorapi.Reminder) error {
	return w.handleReminder(ctx, reminder)
}

// InvokeTimer implements actors.InternalActor
func (w *workflow) InvokeTimer(ctx context.Context, reminder *actorapi.Reminder) error {
	return errors.New("timers are not implemented")
}

func (w *workflow) Completed() bool {
	return w.completed.Load()
}

func (w *workflow) InvokeStream(ctx context.Context, req *internalsv1pb.InternalInvokeRequest, stream chan<- *internalsv1pb.InternalInvokeResponse) error {
	return w.handleStream(ctx, req, stream)
}

// DeactivateActor implements actors.InternalActor
func (w *workflow) Deactivate() error {
	w.cleanup()
	log.Debugf("Workflow actor '%s': deactivated", w.actorID)
	return nil
}

// Key returns the key for this unique actor.
func (w *workflow) Key() string {
	return w.actorType + actorapi.DaprSeparator + w.actorID
}

// Type returns the type for this unique actor.
func (w *workflow) Type() string {
	return w.actorType
}

// ID returns the ID for this unique actor.
func (w *workflow) ID() string {
	return w.actorID
}
