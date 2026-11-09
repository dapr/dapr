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
	suite.Register(new(rerun))
}

// rerun reruns a completed workflow from its first event, as a new instance,
// while no worker is connected.
//   - off: fails, no worker connected. No new instance.
//   - on:  accepted. The new instance reads RUNNING right away (a staged
//     start reads PENDING), and it runs once a worker connects.
type rerun struct{ stagedops.Modes }

func (r *rerun) Run(t *testing.T, ctx context.Context) {
	r.RunModes(t, ctx, func(t *testing.T, m stagedops.Mode) {
		const (
			id    = api.InstanceID("rerun")
			newID = api.InstanceID("rerun-new")
		)
		m.StartWith(t, ctx, 0, stagedops.WfActivity, stagedops.StatusCompleted, id)
		mgmt := m.Mgmt(t, ctx, 0)

		got := m.Call(ctx, func(ctx context.Context) error {
			_, err := mgmt.RerunWorkflowFromEvent(ctx, id, 0, api.WithRerunNewInstanceID(newID))
			return err
		})
		assert.Equal(t, stagedops.Want(m.Staged, stagedops.NoWorker, stagedops.Accepted), got)
		assert.Equal(t, stagedops.Want(m.Staged, stagedops.StatusNotFound, stagedops.StatusRunning), stagedops.StatusOf(ctx, mgmt, newID))

		m.Connect(t, ctx, 0)
		stagedops.WaitStatus(t, ctx, mgmt, newID, stagedops.Want(m.Staged, stagedops.StatusNotFound, stagedops.StatusCompleted))
	})
}
