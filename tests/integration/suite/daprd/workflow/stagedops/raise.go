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
	suite.Register(new(raise))
}

// raise raises an event to a running workflow while no worker is connected.
//   - off: fails, no worker connected. The event is not kept.
//   - on:  accepted. The workflow gets the event once a worker connects.
type raise struct{ stagedops.Modes }

func (r *raise) Run(t *testing.T, ctx context.Context) {
	r.RunModes(t, ctx, func(t *testing.T, m stagedops.Mode) {
		const id = api.InstanceID("raise")
		m.StartWith(t, ctx, 0, stagedops.WfEvent, stagedops.StatusRunning, id)
		mgmt := m.Mgmt(t, ctx, 0)

		got := m.Call(ctx, func(ctx context.Context) error {
			return mgmt.RaiseEvent(ctx, id, stagedops.EvGo, api.WithEventPayload("raised"))
		})
		assert.Equal(t, stagedops.Want(m.Staged, stagedops.NoWorker, stagedops.Accepted), got)
		assert.Equal(t, stagedops.StatusRunning, stagedops.StatusOf(ctx, mgmt, id))

		if !m.Staged {
			return
		}

		m.Connect(t, ctx, 0)
		stagedops.WaitStatus(t, ctx, mgmt, id, stagedops.StatusCompleted)
		meta, err := mgmt.FetchWorkflowMetadata(ctx, id)
		require.NoError(t, err)
		assert.JSONEq(t, `"raised"`, meta.GetOutput().GetValue())
	})
}
