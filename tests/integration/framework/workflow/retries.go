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

package workflow

import (
	"github.com/dapr/durabletask-go/api/protos"
)

// ChildWorkflowRetryChains groups the ChildWorkflowInstanceCreatedEvents in a
// workflow history into retry chains. Each event is keyed by its retry-chain
// correlation key: RetryParentInstanceInfo.InstanceID when set (a retry
// attempt), otherwise the event's own InstanceId (the first attempt). This is
// exactly how a consumer correlates a child call's retry attempts with their
// originating attempt. Events within each chain preserve their history
// (chronological) order.
func ChildWorkflowRetryChains(events []*protos.HistoryEvent) map[string][]*protos.ChildWorkflowInstanceCreatedEvent {
	chains := make(map[string][]*protos.ChildWorkflowInstanceCreatedEvent)
	for _, ev := range events {
		cc := ev.GetChildWorkflowInstanceCreated()
		if cc == nil {
			continue
		}
		key := cc.GetInstanceId()
		if rp := cc.GetRetryParentInstanceInfo(); rp != nil {
			key = rp.GetInstanceID()
		}
		chains[key] = append(chains[key], cc)
	}
	return chains
}
