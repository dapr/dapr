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
	"unicode"
)

type ActorTypeBuilder struct {
	ns string
}

func NewActorTypeBuilder(namespace string) *ActorTypeBuilder {
	return &ActorTypeBuilder{
		ns: namespace,
	}
}

// ValidAppID reports whether appID is safe to interpolate into an actor type
// name. The character set is restricted like instance IDs, in particular
// rejecting '.' so a caller cannot smuggle extra segments into the derived
// type "dapr.internal.<namespace>.<appID>.workflow".
func ValidAppID(appID string) bool {
	for _, c := range appID {
		if !unicode.IsLetter(c) && c != '_' && c != '-' && !unicode.IsDigit(c) {
			return false
		}
	}
	return true
}

func (a *ActorTypeBuilder) Workflow(appID string) string {
	return "dapr.internal." + a.ns + "." + appID + ".workflow"
}

func (a *ActorTypeBuilder) Activity(appID string) string {
	return "dapr.internal." + a.ns + "." + appID + ".activity"
}

// ActivityIDSeparator splits an activity actor ID into its parent workflow
// instance ID, task ID and generation. Identifiers that reach a durable
// artifact or another daprd are wire format: readers must accept every shape
// a released version has written, and writers may only add.
const ActivityIDSeparator = "::"

// ActivityActorID returns the activity actor ID for a scheduled task. The
// executor rendezvous actor for the task deliberately uses the same ID
// (ClusterTasksBackend): placement hashes only the actor ID and all workflow
// actor types are registered by the same hosts, so equal IDs resolve to equal
// hosts across actor types, co-locating the rendezvous with the activity
// actor and its pending-task waiter. The trailing 0 is a fixed generation
// component present so the ID has the same shape on every release that reads
// it.
func ActivityActorID(workflowID string, taskID int32) string {
	return workflowID + ActivityIDSeparator + strconv.Itoa(int(taskID)) + ActivityIDSeparator + "0"
}
