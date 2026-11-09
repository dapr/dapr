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
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/process/workflow/stagedops"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
)

func init() {
	suite.Register(new(resume))
}

// resume resumes a suspended workflow while no worker is connected.
//   - off: fails, no worker connected.
//   - on:  accepted. The instance stays SUSPENDED until a worker connects,
//     then it is RUNNING.
type resume struct{ stagedops.Modes }

func (r *resume) Run(t *testing.T, ctx context.Context) {
	r.RunModes(t, ctx, func(t *testing.T, m stagedops.Mode) {
		const id = api.InstanceID("resume")
		worker := m.Connect(t, ctx, 0)
		mgmt := m.Mgmt(t, ctx, 0)
		_, err := mgmt.ScheduleNewWorkflow(ctx, stagedops.WfEvent, api.WithInstanceID(id))
		require.NoError(t, err)
		stagedops.WaitStatus(t, ctx, mgmt, id, stagedops.StatusRunning)
		require.NoError(t, mgmt.SuspendWorkflow(ctx, id, "suspend"))
		stagedops.WaitStatus(t, ctx, mgmt, id, stagedops.StatusSuspended)
		worker.Disconnect(t)
		m.WaitNoWorker(t, ctx, 0)

		got := m.Call(ctx, func(ctx context.Context) error {
			return mgmt.ResumeWorkflow(ctx, id, "resume")
		})
		assert.Equal(t, stagedops.Want(m.Staged, stagedops.NoWorker, stagedops.Accepted), got)
		assert.Equal(t, stagedops.StatusSuspended, stagedops.StatusOf(ctx, mgmt, id))

		if !m.Staged {
			return
		}

		m.Connect(t, ctx, 0)
		stagedops.WaitStatus(t, ctx, mgmt, id, stagedops.StatusRunning)
	})
}
