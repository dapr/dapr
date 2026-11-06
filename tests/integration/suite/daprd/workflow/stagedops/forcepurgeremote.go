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
	suite.Register(new(forcepurgeremote))
}

// forcepurgeremote runs two daprds of one app with no worker, per mode. It
// force purges a running workflow through the daprd that does not hold the
// workflow's actor, then raises an event to it twice. A force purge stops the
// workflow actor on the daprd that serves the purge and asks the daprd that
// holds it to evict it.
//   - off: no daprd holds the actor. The force purge works and both raises
//     fail, no worker connected.
//   - on:  a staged raise has loaded the actor on the other daprd, which
//     evicts it for the purge. Both raises fail with "no such instance
//     exists".
type forcepurgeremote struct{ stagedops.Modes }

func (r *forcepurgeremote) Setup(t *testing.T) []framework.Option {
	appIDs := map[bool]string{false: uuid.NewString(), true: uuid.NewString()}
	return r.SetupN(t, 2, func(staged bool, _ int) []daprd.Option {
		return []daprd.Option{daprd.WithAppID(appIDs[staged])}
	})
}

func (r *forcepurgeremote) Run(t *testing.T, ctx context.Context) {
	r.RunModes(t, ctx, func(t *testing.T, m stagedops.Mode) {
		const id = api.InstanceID("forcepurgeremote")
		m.StartWith(t, ctx, 0, stagedops.WfEvent, stagedops.StatusRunning, id)
		m.WaitNoWorker(t, ctx, 1)
		purger, mgmt := m.PurgeThroughNonOwner(t, ctx, id)

		got := m.Call(ctx, func(ctx context.Context) error {
			return mgmt[purger].RaiseEvent(ctx, id, stagedops.EvGo, api.WithEventPayload("after"))
		})
		assert.Equal(t, stagedops.Want(m.Staged, stagedops.NoWorker, stagedops.NotFound), got)
		assert.Equal(t, stagedops.StatusNotFound, stagedops.StatusOf(ctx, mgmt[purger], id))

		got = m.Call(ctx, func(ctx context.Context) error {
			return mgmt[purger].RaiseEvent(ctx, id, stagedops.EvGo, api.WithEventPayload("again"))
		})
		assert.Equal(t, stagedops.Want(m.Staged, stagedops.NoWorker, stagedops.NotFound), got)
		assert.Equal(t, stagedops.StatusNotFound, stagedops.StatusOf(ctx, mgmt[purger], id))
	})
}
