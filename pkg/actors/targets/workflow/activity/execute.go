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
	"fmt"
	"strings"

	actorsapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/activity/inflight"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/durabletask-go/backend"
)

func (a *activity) executeActivity(ctx context.Context, name string, taskEvent *backend.HistoryEvent) error {
	activityName := ""
	if ts := taskEvent.GetTaskScheduled(); ts != nil {
		activityName = ts.GetName()
	} else {
		return fmt.Errorf("invalid activity task event: '%s'", taskEvent.String())
	}

	endIndex := strings.Index(a.actorID, "::")
	if endIndex < 0 {
		return fmt.Errorf("invalid activity actor ID: '%s'", a.actorID)
	}
	workflowID := a.actorID[0:endIndex]

	key := inflight.Key(a.actorID, taskEvent)
	call, owner := a.inflight.Acquire(key)
	if !owner {
		// A previous reminder for this activity scheduling is already in
		// flight (or just finished and its outcome is still cached). Wait
		// for its result and surface the same outcome so the scheduler's
		// retry can be acked without dispatching the activity to the SDK
		// again. The owner is responsible for posting the result to the
		// workflow actor.
		log.Debugf("Activity actor '%s': following in-flight execution of '%s'", a.actorID, name)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-call.Done():
			return call.Err()
		}
	}

	return a.runOwned(ctx, key, call, name, activityName, workflowID, taskEvent)
}

func (f *factory) actorNotReachable(ctx context.Context, wfActorType, workflowID string) bool {
	_, _, cancel, err := f.placement.LookupActor(ctx, &actorsapi.LookupActorRequest{
		ActorType: wfActorType,
		ActorID:   workflowID,
	})
	if cancel != nil {
		cancel(nil)
	}
	return errors.Is(err, messages.ErrActorNoAddress)
}
