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

package stagedops

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
)

func init() {
	suite.Register(new(terminatestartoff))
	suite.Register(new(terminatestarton))
}

type (
	terminatestartoff struct{ terminatestart }
	terminatestarton  struct{ terminatestart }
)

func (r *terminatestarton) Setup(t *testing.T) []framework.Option {
	r.staged = true
	return r.terminatestart.Setup(t)
}

// terminatestart terminates a running workflow and then starts a new one
// under the same ID, while no worker is connected.
//   - off: both fail, no worker connected.
//   - on:  the terminate is accepted, but the start fails with "already
//     exists": the instance stays RUNNING until a worker applies the
//     terminate. Probably unwanted, since with a worker the same two calls
//     both succeed.
type terminatestart struct{ mode }

func (r *terminatestart) Run(t *testing.T, ctx context.Context) {
	r.wf.WaitUntilRunning(t, ctx)
	addWorkflows(t, r.wf.Registry())

	const id = api.InstanceID("terminatestart")
	startWith(t, ctx, r.wf, 0, r.staged, wfEvent, statusRunning, id)
	mgmt := r.wf.ManagementClient(t, ctx)

	got := call(ctx, r.staged, func(ctx context.Context) error {
		return mgmt.TerminateWorkflow(ctx, id)
	})
	assert.Equal(t, want(r.staged, noWorker, accepted), got)

	got = call(ctx, r.staged, func(ctx context.Context) error {
		_, err := mgmt.ScheduleNewWorkflow(ctx, wfDone, api.WithInstanceID(id))
		return err
	})
	assert.Equal(t, want(r.staged, noWorker, alreadyExists), got)
	assert.Equal(t, statusRunning, statusOf(ctx, mgmt, id))
}
