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

package activity

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/dapr/dapr/pkg/actors"
	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/reminders"
	"github.com/dapr/dapr/pkg/actors/router"
	"github.com/dapr/dapr/pkg/actors/state"
	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/dapr/pkg/actors/targets"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/kit/logger"
)

var (
	log = logger.NewLogger("dapr.runtime.actors.targets.activity")

	activityCache = &sync.Pool{
		New: func() any {
			var a *activity
			return a
		},
	}
)

type activity struct {
	appID             string
	actorID           string
	actorType         string
	workflowActorType string

	table     table.Interface
	router    router.Interface
	state     state.Interface
	reminders reminders.Interface

	scheduler          todo.ActivityScheduler
	reminderInterval   time.Duration
	schedulerReminders bool

	lock chan struct{}
}

type Options struct {
	AppID              string
	ActivityActorType  string
	WorkflowActorType  string
	ReminderInterval   *time.Duration
	Scheduler          todo.ActivityScheduler
	Actors             actors.Interface
	SchedulerReminders bool
}

func Factory(ctx context.Context, opts Options) (targets.Factory, error) {
	table, err := opts.Actors.Table(ctx)
	if err != nil {
		return nil, err
	}

	router, err := opts.Actors.Router(ctx)
	if err != nil {
		return nil, err
	}

	state, err := opts.Actors.State(ctx)
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
		a := activityCache.Get().(*activity)
		if a == nil {
			a = &activity{
				appID:              opts.AppID,
				actorID:            actorID,
				actorType:          opts.ActivityActorType,
				workflowActorType:  opts.WorkflowActorType,
				reminderInterval:   reminderInterval,
				table:              table,
				router:             router,
				state:              state,
				reminders:          reminders,
				scheduler:          opts.Scheduler,
				schedulerReminders: opts.SchedulerReminders,
				lock:               make(chan struct{}, 1),
			}
		} else {
			a.actorID = actorID
		}

		return a
	}, nil
}

// InvokeMethod implements actors.InternalActor and schedules the background execution of a workflow activity.
// Activities are scheduled by workflows and can execute for arbitrary lengths of time. Instead of executing
// activity logic directly, InvokeMethod creates a reminder that executes the activity logic. InvokeMethod
// returns immediately after creating the reminder, enabling the workflow to continue processing other events
// in parallel.
func (a *activity) InvokeMethod(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
	select {
	case a.lock <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-a.lock }()
	return a.handleInvoke(ctx, req)
}

// InvokeReminder implements actors.InternalActor and executes the activity logic.
func (a *activity) InvokeReminder(ctx context.Context, reminder *actorapi.Reminder) error {
	if !reminder.SkipLock {
		select {
		case a.lock <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		defer func() { <-a.lock }()
	}
	return a.handleReminder(ctx, reminder)
}

// InvokeTimer implements actors.InternalActor
func (a *activity) InvokeTimer(ctx context.Context, reminder *actorapi.Reminder) error {
	return errors.New("timers are not implemented")
}

// DeactivateActor implements actors.InternalActor
func (a *activity) Deactivate(context.Context) error {
	log.Debugf("Activity actor '%s': deactivated", a.actorID)
	activityCache.Put(a)
	return nil
}

func (a *activity) InvokeStream(context.Context, *internalsv1pb.InternalInvokeRequest, chan<- *internalsv1pb.InternalInvokeResponse) error {
	return errors.New("not implemented")
}

// Key returns the key for this unique actor.
func (a *activity) Key() string {
	return a.actorType + actorapi.DaprSeparator + a.actorID
}

// Type returns the type of actor.
func (a *activity) Type() string {
	return a.actorType
}

// ID returns the ID of the actor.
func (a *activity) ID() string {
	return a.actorID
}
