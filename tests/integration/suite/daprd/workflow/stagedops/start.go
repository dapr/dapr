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
	suite.Register(new(start))
}

// start schedules a new workflow while no worker has ever connected.
//   - off: fails, no worker connected. Nothing is kept.
//   - on:  accepted at once. The instance is PENDING and runs once a worker
//     connects.
type start struct{ stagedops.Modes }

func (s *start) Run(t *testing.T, ctx context.Context) {
	s.RunModes(t, ctx, func(t *testing.T, m stagedops.Mode) {
		m.WaitNoWorker(t, ctx, 0)
		mgmt := m.Mgmt(t, ctx, 0)

		const id = api.InstanceID("start")
		got := m.Call(ctx, func(ctx context.Context) error {
			_, err := mgmt.ScheduleNewWorkflow(ctx, stagedops.WfDone, api.WithInstanceID(id))
			return err
		})
		assert.Equal(t, stagedops.Want(m.Staged, stagedops.NoWorker, stagedops.Accepted), got)
		assert.Equal(t, stagedops.Want(m.Staged, stagedops.StatusNotFound, stagedops.StatusPending), stagedops.StatusOf(ctx, mgmt, id))

		m.Connect(t, ctx, 0)
		stagedops.WaitStatus(t, ctx, mgmt, id, stagedops.Want(m.Staged, stagedops.StatusNotFound, stagedops.StatusCompleted))
	})
}
