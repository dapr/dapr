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
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
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
	daprds          int
	skipDB          bool
	mtls            bool
	signing         bool
	signingDisabled []int
	clustered       *bool
	fastPath        *bool

	orchestrators     []orchestratorConfig
	activities        []activityConfig
	daprdOptions      []daprdOptionConfig
	schedulerOptions  []scheduler.Option
	placementOptions  []placement.Option
	schedulerInstance *scheduler.Scheduler
	schedulerAddress  *string

	schedulerPlacement *bool
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

// WithMTLS spins up a Sentry process for mTLS and enables the
// WorkflowHistorySigning feature flag on every daprd in the workflow.
func WithMTLS(t *testing.T) Option {
	t.Helper()
	return func(o *options) {
		o.mtls = true
	}
}

// WithHistorySigning enables the WorkflowHistorySigning feature flag on
// every daprd in the workflow. History signing needs the Sentry-issued
// workload identity for its attestation and signing keys, so this also
// enables the mTLS setup of WithMTLS. Prefer this over WithMTLS in tests
// that are about signing behavior, so the intent is explicit at the call
// site.
func WithHistorySigning(t *testing.T) Option {
	t.Helper()
	return func(o *options) {
		o.signing = true
		o.mtls = true
	}
}

// WithSigningDisabledN excludes the daprd at the given index from having
// the WorkflowHistorySigning feature flag set. Has no effect without
// WithMTLS or WithHistorySigning.
func WithSigningDisabledN(index int) Option {
	return func(o *options) {
		o.signingDisabled = append(o.signingDisabled, index)
	}
}

// WithClusteredDeployment explicitly enables or disables the
// WorkflowsClusteredDeployment feature flag on every daprd in the workflow,
// overriding the DAPR_INTEGRATION_WORKFLOW_CLUSTERED environment variable.
// WithSchedulerPlacement serves actor placement from the scheduler: no
// placement process runs.
func WithSchedulerPlacement() Option {
	return func(o *options) {
		enabled := true
		o.schedulerPlacement = &enabled
	}
}

// WithPlacementService pins actor placement to the placement service,
// overriding DAPR_INTEGRATION_SCHEDULER_PLACEMENT.
func WithPlacementService() Option {
	return func(o *options) {
		disabled := false
		o.schedulerPlacement = &disabled
	}
}

func WithClusteredDeployment(enabled bool) Option {
	return func(o *options) {
		o.clustered = &enabled
	}
}

// WithFastPath explicitly enables or disables the WorkflowsFastPath feature
// flag on every daprd in the workflow, overriding the
// DAPR_INTEGRATION_WORKFLOW_FASTPATH environment variable.
func WithFastPath(enabled bool) Option {
	return func(o *options) {
		o.fastPath = &enabled
	}
}

func WithSchedulerOptions(opts ...scheduler.Option) Option {
	return func(o *options) {
		o.schedulerOptions = append(o.schedulerOptions, opts...)
	}
}

// WithSchedulerInstance lets a test supply a pre-constructed scheduler. The
// framework uses this scheduler instead of creating its own and skips
// adding it to its process list (the caller is responsible for that).
// Combine with WithSchedulerAddress when interposing a proxy.
func WithSchedulerInstance(sched *scheduler.Scheduler) Option {
	return func(o *options) {
		o.schedulerInstance = sched
	}
}

// WithSchedulerAddress overrides the address used for the daprd's
// --scheduler-host-address flag. Use this to point daprd at a proxy that
// fronts the real scheduler.
func WithSchedulerAddress(addr string) Option {
	return func(o *options) {
		o.schedulerAddress = &addr
	}
}

func WithPlacementOptions(opts ...placement.Option) Option {
	return func(o *options) {
		o.placementOptions = append(o.placementOptions, opts...)
	}
}
