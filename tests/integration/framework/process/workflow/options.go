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

	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/durabletask-go/task"
)

type Option func(*options)

type orchestratorConfig struct {
	index int
	name  string
	fn    func(*task.WorkflowContext) (any, error)
}

type activityConfig struct {
	index int
	name  string
	fn    func(task.ActivityContext) (any, error)
}

type daprdOptionConfig struct {
	index int
	opts  []daprd.Option
}

type options struct {
	daprds int
	skipDB bool
	mtls   bool

	orchestrators []orchestratorConfig
	activities    []activityConfig
	daprdOptions  []daprdOptionConfig
}

func WithAddOrchestrator(t *testing.T, name string, or func(*task.WorkflowContext) (any, error)) Option {
	t.Helper()
	return WithAddWorkflowN(t, 0, name, or)
}

func WithAddWorkflowN(t *testing.T, index int, name string, or func(*task.WorkflowContext) (any, error)) Option {
	t.Helper()

	return func(o *options) {
		o.orchestrators = append(o.orchestrators, orchestratorConfig{
			index: index,
			name:  name,
			fn:    or,
		})
	}
}

func WithAddActivity(t *testing.T, name string, a func(task.ActivityContext) (any, error)) Option {
	t.Helper()
	return WithAddActivityN(t, 0, name, a)
}

func WithAddActivityN(t *testing.T, index int, name string, a func(task.ActivityContext) (any, error)) Option {
	t.Helper()

	return func(o *options) {
		o.activities = append(o.activities, activityConfig{
			index: index,
			name:  name,
			fn:    a,
		})
	}
}

func WithDaprds(daprds int) Option {
	return func(o *options) {
		o.daprds = daprds
	}
}

func WithDaprdOptions(index int, opts ...daprd.Option) Option {
	return func(o *options) {
		o.daprdOptions = append(o.daprdOptions, daprdOptionConfig{
			index: index,
			opts:  opts,
		})
	}
}

func WithNoDB() Option {
	return func(o *options) {
		o.skipDB = true
	}
}

// WithMTLS enables mTLS on all daprd instances by spinning up a Sentry
// process AND turns on the WorkflowHistorySigning feature flag on each
// daprd. Propagation is gated on signing being active, and signing requires
// mTLS for the SPIFFE identity, so these two are coupled for propagation
// tests.
func WithMTLS(t *testing.T) Option {
	t.Helper()
	return func(o *options) {
		o.mtls = true
	}
}
