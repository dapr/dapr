/*
Copyright 2026 The Dapr Authors
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
	"errors"

	"github.com/dapr/durabletask-go/backend"
)

type dispatchResult struct {
	failedEventIDs map[int32]struct{}
	err            error
}

func (r *dispatchResult) recordFailure(eventID int32, err error) {
	if r.failedEventIDs == nil {
		r.failedEventIDs = make(map[int32]struct{})
	}
	r.failedEventIDs[eventID] = struct{}{}
	r.err = errors.Join(r.err, err)
}

func hasRemoteTasks(es []*backend.HistoryEvent) bool {
	for _, e := range es {
		if router := e.GetRouter(); router != nil && router.TargetAppID != nil {
			return true
		}
	}
	return false
}

func hasRemoteMessages(msgs []*backend.WorkflowRuntimeStateMessage) bool {
	for _, msg := range msgs {
		if router := msg.GetHistoryEvent().GetRouter(); router != nil && router.TargetAppID != nil {
			return true
		}
	}
	return false
}

func isDispatchableEvent(e *backend.HistoryEvent) bool {
	return e.GetTaskScheduled() != nil || e.GetChildWorkflowInstanceCreated() != nil
}
