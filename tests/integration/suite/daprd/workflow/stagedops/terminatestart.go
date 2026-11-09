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

	"github.com/dapr/dapr/tests/integration/framework/process/workflow/stagedops"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
)

func init() {
	suite.Register(new(terminatestart))
}

// terminatestart terminates a running workflow and then starts a new one
// under the same ID, while no worker is connected.
//   - off: both fail, no worker connected.
//   - on:  the terminate is accepted, but the start fails with "already
//     exists": the instance stays RUNNING until a worker applies the
//     terminate. Probably unwanted, since with a worker the same two calls
//     both succeed.
type terminatestart struct{ stagedops.Modes }

func (r *terminatestart) Run(t *testing.T, ctx context.Context) {
	r.RunModes(t, ctx, func(t *testing.T, m stagedops.Mode) {
		const id = api.InstanceID("terminatestart")
		m.StartWith(t, ctx, 0, stagedops.WfEvent, stagedops.StatusRunning, id)
		mgmt := m.Mgmt(t, ctx, 0)

		got := m.Call(ctx, func(ctx context.Context) error {
			return mgmt.TerminateWorkflow(ctx, id)
		})
		assert.Equal(t, stagedops.Want(m.Staged, stagedops.NoWorker, stagedops.Accepted), got)

		got = m.Call(ctx, func(ctx context.Context) error {
			_, err := mgmt.ScheduleNewWorkflow(ctx, stagedops.WfDone, api.WithInstanceID(id))
			return err
		})
		assert.Equal(t, stagedops.Want(m.Staged, stagedops.NoWorker, stagedops.AlreadyExists), got)
		assert.Equal(t, stagedops.StatusRunning, stagedops.StatusOf(ctx, mgmt, id))
	})
}
