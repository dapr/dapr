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

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/lock"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.targets.activity")

type activity struct {
	*factory
	actorID string
	lock    *lock.Lock
}

// InvokeMethod implements actors.InternalActor and schedules the background execution of a workflow activity.
// Activities are scheduled by workflows and can execute for arbitrary lengths of time. Instead of executing
// activity logic directly, InvokeMethod creates a reminder that executes the activity logic. InvokeMethod
// returns immediately after creating the reminder, enabling the workflow to continue processing other events
// in parallel.
func (a *activity) InvokeMethod(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
	unlock, err := a.lock.ContextLock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()

	return a.handleInvoke(ctx, req)
}

// InvokeReminder implements actors.InternalActor and executes the activity logic.
func (a *activity) InvokeReminder(ctx context.Context, reminder *actorapi.Reminder) error {
	if !reminder.SkipLock {
		unlock, err := a.lock.ContextLock(ctx)
		if err != nil {
			return err
		}
		defer unlock()
	}

	if err := a.handleReminder(ctx, reminder); err != nil {
		return err
	}

	return a.Deactivate(ctx)
}

// InvokeTimer implements actors.InternalActor
func (a *activity) InvokeTimer(ctx context.Context, reminder *actorapi.Reminder) error {
	return errors.New("timers are not implemented")
}

// DeactivateActor implements actors.InternalActor
func (a *activity) Deactivate(context.Context) error {
	a.table.Delete(a.actorID)
	activityCache.Put(a)
	return nil
}

func (a *activity) InvokeStream(context.Context,
	*internalsv1pb.InternalInvokeRequest,
	func(*internalsv1pb.InternalInvokeResponse) (bool, error),
) error {
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
