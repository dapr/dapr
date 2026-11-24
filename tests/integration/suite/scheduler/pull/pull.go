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

package pull

import (
	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/kit/ptr"
)

const (
	namespace = "default"
	appID     = "myapp"
	actorType = "dapr.internal.default.myapp.activity"
)

// sidecar is the initial request of a pull sidecar at addr hosting actorType.
func sidecar(addr string, slots []*schedulerv1.PullSlots, inflight ...*schedulerv1.ActorReminderRef) *schedulerv1.WatchJobsRequestInitial {
	return &schedulerv1.WatchJobsRequestInitial{
		AppId:             appID,
		Namespace:         namespace,
		AcceptJobTypes:    []schedulerv1.JobTargetType{schedulerv1.JobTargetType_JOB_TARGET_TYPE_ACTOR_REMINDER},
		ActorTypes:        []string{actorType},
		ActorAddress:      ptr.Of(addr),
		PullSlots:         slots,
		InflightReminders: inflight,
	}
}

func free(n uint32) []*schedulerv1.PullSlots {
	return []*schedulerv1.PullSlots{{ActorType: actorType, Free: n}}
}

func reminder(actorID, name string) *schedulerv1.ActorReminderRef {
	return &schedulerv1.ActorReminderRef{ActorType: actorType, ActorId: actorID, Name: name}
}
