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

package common

import (
	"strconv"
)

type ActorTypeBuilder struct {
	ns string
}

func NewActorTypeBuilder(namespace string) *ActorTypeBuilder {
	return &ActorTypeBuilder{
		ns: namespace,
	}
}

func (a *ActorTypeBuilder) Workflow(appID string) string {
	return "dapr.internal." + a.ns + "." + appID + ".workflow"
}

func (a *ActorTypeBuilder) Activity(appID string) string {
	return "dapr.internal." + a.ns + "." + appID + ".activity"
}

// ActivityActorID returns the activity actor ID for a scheduled task. The
// executor rendezvous actor for the task deliberately uses the same ID
// (ClusterTasksBackend): placement hashes only the actor ID and all workflow
// actor types are registered by the same hosts, so equal IDs resolve to equal
// hosts across actor types, co-locating the rendezvous with the activity
// actor and its pending-task waiter.
func ActivityActorID(workflowID string, taskID int32) string {
	return workflowID + "::" + strconv.Itoa(int(taskID))
}
