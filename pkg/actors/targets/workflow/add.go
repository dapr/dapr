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

	// For activity completion events, we want to create the reminder on the same app where this workflow actor is hosted
	targetApp := w.appID
	if e.GetRouter() != nil {
		// For activity completion events, use the source app from the router
		targetApp = e.GetRouter().GetSource()
		if targetApp == "" {
			targetApp = w.appID
		}
	}

	if _, err := w.createReminder(ctx, "new-event", nil, 0, targetApp); err != nil {
		return err
	}

	return nil
}
