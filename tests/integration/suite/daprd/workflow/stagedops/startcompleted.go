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
	suite.Register(new(startcompleted))
}

// startcompleted starts a workflow again under the ID of an instance that has
// completed, while no worker is connected.
//   - off: fails, no worker connected. The old instance stays COMPLETED.
//   - on:  accepted. The new run replaces the old instance, PENDING until a
//     worker connects, then it runs.
type startcompleted struct{ stagedops.Modes }

func (s *startcompleted) Run(t *testing.T, ctx context.Context) {
	s.RunModes(t, ctx, func(t *testing.T, m stagedops.Mode) {
		const id = api.InstanceID("startcompleted")
		m.StartWith(t, ctx, 0, stagedops.WfDone, stagedops.StatusCompleted, id)
		mgmt := m.Mgmt(t, ctx, 0)

		got := m.Call(ctx, func(ctx context.Context) error {
			_, err := mgmt.ScheduleNewWorkflow(ctx, stagedops.WfDone, api.WithInstanceID(id))
			return err
		})
		assert.Equal(t, stagedops.Want(m.Staged, stagedops.NoWorker, stagedops.Accepted), got)
		assert.Equal(t, stagedops.Want(m.Staged, stagedops.StatusCompleted, stagedops.StatusPending), stagedops.StatusOf(ctx, mgmt, id))

		m.Connect(t, ctx, 0)
		stagedops.WaitStatus(t, ctx, mgmt, id, stagedops.StatusCompleted)
	})
}
