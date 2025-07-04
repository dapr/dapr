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

package orchestrator

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

var (
	log = logger.NewLogger("dapr.runtime.actors.targets.orchestrator")

	orchestratorCache = sync.Pool{
		New: func() any {
			var o *orchestrator
			return o
		},
	}
)

type EventSink func(*backend.OrchestrationMetadata)

type orchestrator struct {
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
	lock                  chan struct{}
	closeCh               chan struct{}
	closed                atomic.Bool
	wg                    sync.WaitGroup
}

type Options struct {
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

func Factory(ctx context.Context, opts Options) (targets.Factory, error) {
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

	reminderInterval := time.Minute * 1

	if opts.ReminderInterval != nil {
		reminderInterval = *opts.ReminderInterval
	}

	return func(actorID string) targets.Interface {
		o := orchestratorCache.Get().(*orchestrator)
		if o == nil {
			o = &orchestrator{
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
				lock:               make(chan struct{}, 1),
			}
		} else {
			// Wait for any previous operations to complete before reusing the workflow
			// instance.
			o.actorID = actorID
			o.ometaBroadcaster = broadcaster.New[*backend.OrchestrationMetadata]()
			o.closeCh = make(chan struct{})
			o.closed.Store(false)
		}

		if opts.EventSink != nil {
			ch := make(chan *backend.OrchestrationMetadata)
			go o.runEventSink(ch, opts.EventSink)
			// We use a Background context since this subscription should be
			// maintained for the entire lifecycle of this workflow actor. The
			// subscription will be shutdown during the actor deactivation.
			o.ometaBroadcaster.Subscribe(context.Background(), ch)
		}
		return o
	}, nil
}

// InvokeMethod implements actors.InternalActor
func (o *orchestrator) InvokeMethod(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
	select {
	case o.lock <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-o.lock }()
	return o.handleInvoke(ctx, req)
}

// InvokeReminder implements actors.InternalActor
func (o *orchestrator) InvokeReminder(ctx context.Context, reminder *actorapi.Reminder) error {
	select {
	case o.lock <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-o.lock }()
	return o.handleReminder(ctx, reminder)
}

// InvokeTimer implements actors.InternalActor
func (o *orchestrator) InvokeTimer(ctx context.Context, reminder *actorapi.Reminder) error {
	return errors.New("timers are not implemented")
}

func (o *orchestrator) Completed() bool {
	return o.completed.Load()
}

func (o *orchestrator) InvokeStream(ctx context.Context, req *internalsv1pb.InternalInvokeRequest, stream chan<- *internalsv1pb.InternalInvokeResponse) error {
	o.wg.Add(1)
	defer o.wg.Done()
	return o.handleStream(ctx, req, stream)
}

// DeactivateActor implements actors.InternalActor
func (o *orchestrator) Deactivate(ctx context.Context) error {
	o.lock <- struct{}{}
	defer func() { <-o.lock }()

	o.cleanup()
	log.Debugf("Workflow actor '%s': deactivated", o.actorID)
	return nil
}

// Key returns the key for this unique actor.
func (o *orchestrator) Key() string {
	return o.actorType + actorapi.DaprSeparator + o.actorID
}

// Type returns the type for this unique actor.
func (o *orchestrator) Type() string {
	return o.actorType
}

// ID returns the ID for this unique actor.
func (o *orchestrator) ID() string {
	return o.actorID
}
