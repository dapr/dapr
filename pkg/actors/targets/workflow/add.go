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

package workflow

import (
	"context"

	"google.golang.org/protobuf/proto"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
)

func (w *workflow) addWorkflowEvent(ctx context.Context, historyEventBytes []byte) error {
	state, _, err := w.loadInternalState(ctx)
	if err != nil {
		return err
	}
	if state == nil {
		return api.ErrInstanceNotFound
	}

	var e backend.HistoryEvent
	err = proto.Unmarshal(historyEventBytes, &e)
	if e.GetTaskCompleted() != nil || e.GetTaskFailed() != nil {
		w.activityResultAwaited.CompareAndSwap(true, false)
	}
	if err != nil {
		return err
	}
	log.Debugf("Workflow actor '%s': adding event to the workflow inbox", w.actorID)
	state.AddToInbox(&e)

	if err := w.saveInternalState(ctx, state); err != nil {
		return err
	}

	// TODO: @cassie, see about using e.GetRouter instead of the following, but it works
	var targetApp string
	// Get target app
	if taskCompleted := e.GetTaskCompleted(); taskCompleted != nil && taskCompleted.GetRouter() != nil {
		targetApp = taskCompleted.GetRouter().GetTarget()
	} else if taskFailed := e.GetTaskFailed(); taskFailed != nil && taskFailed.GetRouter() != nil {
		targetApp = taskFailed.GetRouter().GetTarget()
	} else if taskScheduled := e.GetTaskScheduled(); taskScheduled != nil && taskScheduled.GetRouter() != nil {
		targetApp = taskScheduled.GetRouter().GetTarget()
	} else {
		// Default to current app if no router info
		targetApp = w.appID
	}

	if _, err := w.createReminder(ctx, "new-event", nil, 0, targetApp); err != nil {
		return err
	}

	return nil
}
