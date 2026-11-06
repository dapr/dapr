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
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
)

func init() {
	suite.Register(new(forcepurgeremote))
}

// forcepurgeremote runs two daprds of one app with no worker, per mode. It
// force purges a running workflow through the daprd that does not hold the
// workflow's actor, then raises an event to it twice. A force purge stops the
// workflow actor only on the daprd that serves the purge.
//   - off: no daprd holds the actor. The force purge works and both raises
//     fail, no worker connected.
//   - on:  a staged raise has loaded the actor on the other daprd, which
//     keeps it after the purge. The first raise reaches that actor and fails
//     with an internal "possible etag mismatch" error; the actor then drops
//     its state, and the second raise fails with "no such instance exists".
//     The instance stays purged. Probably unwanted: the caller gets internal
//     errors where it would get "not found" with the preview off.
type forcepurgeremote struct{ modes }

func (r *forcepurgeremote) Setup(t *testing.T) []framework.Option {
	appIDs := map[bool]string{false: uuid.NewString(), true: uuid.NewString()}
	return r.setup(t, 2, func(staged bool, _ int) []daprd.Option {
		return []daprd.Option{daprd.WithAppID(appIDs[staged])}
	})
}

func (r *forcepurgeremote) Run(t *testing.T, ctx context.Context) {
	r.run(t, ctx, func(t *testing.T, m mode) {
		const id = api.InstanceID("forcepurgeremote")
		m.startWith(t, ctx, 0, wfEvent, statusRunning, id)
		m.waitNoWorker(t, ctx, 1)
		mgmt := []*client.TaskHubGrpcClient{m.mgmt(t, ctx, 0), m.mgmt(t, ctx, 1)}

		// With the preview on, a staged raise loads the workflow actor on the
		// daprd which owns it. The event is not the one the workflow waits
		// for.
		got := m.call(ctx, func(ctx context.Context) error {
			return mgmt[0].RaiseEvent(ctx, id, "other")
		})
		assert.Equal(t, want(m.staged, noWorker, accepted), got)

		purger := 1
		if m.staged {
			owner := -1
			require.EventuallyWithT(t, func(c *assert.CollectT) {
				for i := range 2 {
					d := m.idx(i)
					if n, _ := m.wf.DaprN(d).ActiveActorCount(c, ctx, m.wf.WorkflowActorType(d)); n > 0 {
						owner = i
					}
				}
				assert.GreaterOrEqual(c, owner, 0)
			}, time.Second*10, time.Millisecond*50)
			purger = 1 - owner
		}

		got = m.call(ctx, func(ctx context.Context) error {
			return mgmt[purger].PurgeWorkflowState(ctx, id, api.WithForcePurge(true))
		})
		assert.Equal(t, accepted, got)
		assert.Equal(t, statusNotFound, statusOf(ctx, mgmt[purger], id))

		got = m.call(ctx, func(ctx context.Context) error {
			return mgmt[purger].RaiseEvent(ctx, id, evGo, api.WithEventPayload("after"))
		})
		assert.Equal(t, want(m.staged, noWorker, etagMismatch), got)
		assert.Equal(t, statusNotFound, statusOf(ctx, mgmt[purger], id))

		got = m.call(ctx, func(ctx context.Context) error {
			return mgmt[purger].RaiseEvent(ctx, id, evGo, api.WithEventPayload("again"))
		})
		assert.Equal(t, want(m.staged, noWorker, notFound), got)
		assert.Equal(t, statusNotFound, statusOf(ctx, mgmt[purger], id))
	})
}
