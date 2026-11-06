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

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow/stagedops"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
)

func init() {
	suite.Register(new(forcepurgestart))
}

// forcepurgestart runs two daprds of one app with no worker, per mode. It
// force purges a running workflow through the daprd that does not hold the
// workflow's actor, then starts a new workflow under the same ID.
//   - off: no daprd holds the actor. The force purge works and the start
//     fails, no worker connected.
//   - on:  a staged raise has loaded the actor on the other daprd, which
//     evicts it for the purge. Neither daprd has a worker, so the actor reads
//     the instance from the store, finds none, and accepts the start: the
//     new instance is PENDING.
type forcepurgestart struct{ stagedops.Modes }

func (r *forcepurgestart) Setup(t *testing.T) []framework.Option {
	appIDs := map[bool]string{false: uuid.NewString(), true: uuid.NewString()}
	return r.SetupN(t, 2, func(staged bool, _ int) []daprd.Option {
		return []daprd.Option{daprd.WithAppID(appIDs[staged])}
	})
}

func (r *forcepurgestart) Run(t *testing.T, ctx context.Context) {
	r.RunModes(t, ctx, func(t *testing.T, m stagedops.Mode) {
		const id = api.InstanceID("forcepurgestart")
		m.StartWith(t, ctx, 0, stagedops.WfEvent, stagedops.StatusRunning, id)
		m.WaitNoWorker(t, ctx, 1)
		purger, mgmt := m.PurgeThroughNonOwner(t, ctx, id)

		got := m.Call(ctx, func(ctx context.Context) error {
			_, err := mgmt[purger].ScheduleNewWorkflow(ctx, stagedops.WfDone, api.WithInstanceID(id))
			return err
		})
		assert.Equal(t, stagedops.Want(m.Staged, stagedops.NoWorker, stagedops.Accepted), got)
		assert.Equal(t, stagedops.Want(m.Staged, stagedops.StatusNotFound, stagedops.StatusPending), stagedops.StatusOf(ctx, mgmt[purger], id))
	})
}
