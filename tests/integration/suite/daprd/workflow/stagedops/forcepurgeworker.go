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
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow/stagedops"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
)

func init() {
	suite.Register(new(forcepurgeworker))
}

// forcepurgeworker runs two daprds of one app, per mode, with a worker
// connected to the first. It force purges a running workflow, whose actor the
// first daprd holds, through the second, then starts a new workflow under the
// same ID through the first. A force purge deletes the instance's rows, and
// the daprd that holds the actor must not judge the start on its cached copy.
//   - off: both daprds read the instance as gone, and the start is accepted
//     and runs to completion.
//   - on:  the same.
type forcepurgeworker struct{ stagedops.Modes }

func (r *forcepurgeworker) Setup(t *testing.T) []framework.Option {
	appIDs := map[bool]string{false: uuid.NewString(), true: uuid.NewString()}
	return r.SetupN(t, 2, func(staged bool, _ int) []daprd.Option {
		return []daprd.Option{daprd.WithAppID(appIDs[staged])}
	})
}

func (r *forcepurgeworker) Run(t *testing.T, ctx context.Context) {
	r.RunModes(t, ctx, func(t *testing.T, m stagedops.Mode) {
		const id = api.InstanceID("forcepurgeworker")
		m.Connect(t, ctx, 0)
		mgmt := []*client.TaskHubGrpcClient{m.Mgmt(t, ctx, 0), m.Mgmt(t, ctx, 1)}

		_, err := mgmt[0].ScheduleNewWorkflow(ctx, stagedops.WfEvent, api.WithInstanceID(id))
		require.NoError(t, err)
		stagedops.WaitStatus(t, ctx, mgmt[0], id, stagedops.StatusRunning)

		d := m.Idx(0)
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			n, _ := m.WF.DaprN(d).ActiveActorCount(c, ctx, m.WF.WorkflowActorType(d))
			assert.Equal(c, 1, n)
		}, time.Second*10, time.Millisecond*50)

		got := m.Call(ctx, func(ctx context.Context) error {
			return mgmt[1].PurgeWorkflowState(ctx, id, api.WithForcePurge(true))
		})
		assert.Equal(t, stagedops.Accepted, got)
		assert.Equal(t, stagedops.StatusNotFound, stagedops.StatusOf(ctx, mgmt[0], id))
		assert.Equal(t, stagedops.StatusNotFound, stagedops.StatusOf(ctx, mgmt[1], id))

		got = m.Call(ctx, func(ctx context.Context) error {
			_, err := mgmt[0].ScheduleNewWorkflow(ctx, stagedops.WfDone, api.WithInstanceID(id))
			return err
		})
		assert.Equal(t, stagedops.Accepted, got)
		stagedops.WaitStatus(t, ctx, mgmt[0], id, stagedops.StatusCompleted)
	})
}
