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

package actors

import (
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/dapr/utils"
)

// ActorTypes holds the names of the internal actor types an app's workflows
// run on.
type ActorTypes struct {
	Workflow    string
	Activity    string
	Executor    string
	Retentioner string
}

// NewActorTypes returns the names of the workflow actor types of the app
// appID in namespace.
func NewActorTypes(namespace, appID string) ActorTypes {
	prefix := todo.ActorTypePrefix + namespace + utils.DotDelimiter + appID + utils.DotDelimiter
	return ActorTypes{
		Workflow:    prefix + WorkflowNameLabelKey,
		Activity:    prefix + ActivityNameLabelKey,
		Executor:    prefix + ExecutorNameLabelKey,
		Retentioner: prefix + RetentionerNameLabelKey,
	}
}

// All returns the names of all the workflow actor types.
func (t ActorTypes) All() []string {
	return []string{t.Workflow, t.Activity, t.Executor, t.Retentioner}
}
