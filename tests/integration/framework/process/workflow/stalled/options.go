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

package stalled

import "github.com/dapr/durabletask-go/task"

type Option func(*options)

type options struct {
	initialReplica string
	workflows      map[string]task.Orchestrator
	activities     map[string]task.Activity
}

func WithNamedWorkflowReplica(name string, workflow task.Orchestrator) Option {
	return func(o *options) {
		o.workflows[name] = workflow
	}
}

func WithInitialReplica(name string) Option {
	return func(o *options) {
		o.initialReplica = name
	}
}

func WithActivity(name string, activity task.Activity) Option {
	return func(o *options) {
		o.activities[name] = activity
	}
}
