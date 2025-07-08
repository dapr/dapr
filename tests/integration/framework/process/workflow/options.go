/*
Copyright 2024 The Dapr Authors
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
	"testing"

	"github.com/dapr/durabletask-go/task"
)

type Option func(*options)

type options struct {
	daprds          int
	enableScheduler bool

	orchestrators []struct {
		index int
		name  string
		fn    func(*task.OrchestrationContext) (any, error)
	}
	activities []struct {
		index int
		name  string
		fn    func(task.ActivityContext) (any, error)
	}
}

func WithScheduler(enable bool) Option {
	return func(o *options) {
		o.enableScheduler = enable
	}
}

func WithAddOrchestratorN(t *testing.T, index int, name string, or func(*task.OrchestrationContext) (any, error)) Option {
	t.Helper()

	return func(o *options) {
		o.orchestrators = append(o.orchestrators, struct {
			index int
			name  string
			fn    func(*task.OrchestrationContext) (any, error)
		}{index, name, or})
	}
}

func WithAddActivityN(t *testing.T, index int, name string, a func(task.ActivityContext) (any, error)) Option {
	t.Helper()

	return func(o *options) {
		o.activities = append(o.activities, struct {
			index int
			name  string
			fn    func(task.ActivityContext) (any, error)
		}{index, name, a})
	}
}

func WithDaprds(daprds int) Option {
	return func(o *options) {
		o.daprds = daprds
	}
}
