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
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
)

func init() {
	suite.Register(new(forcepurgeremoteoff))
	suite.Register(new(forcepurgeremoteon))
}

type (
	forcepurgeremoteoff struct{ forcepurgeremote }
	forcepurgeremoteon  struct{ forcepurgeremote }
)

func (f *forcepurgeremoteon) Setup(t *testing.T) []framework.Option {
	f.staged = true
	return f.forcepurgeremote.Setup(t)
}

// forcepurgeremote runs two daprds of one app with no worker. It force purges
// a running workflow through the daprd that does not hold the workflow's
// actor, then raises an event to it twice. A force purge stops the workflow
// actor only on the daprd that serves the purge.
//   - off: no daprd holds the actor. The force purge works and both raises
//     fail, no worker connected.
//   - on:  a staged raise has loaded the actor on the other daprd, which
//     keeps it after the purge. The first raise reaches that actor and fails
//     with an internal "possible etag mismatch" error; the actor then drops
//     its state, and the second raise fails with "no such instance exists".
//     The instance stays purged. Probably unwanted: the caller gets internal
//     errors where it would get "not found" with the preview off.
type forcepurgeremote struct{ mode }

func (r *forcepurgeremote) Setup(t *testing.T) []framework.Option {
	appID := uuid.NewString()
	r.wf = newWorkflow(t, r.staged, 2,
		workflow.WithDaprdOptions(0, daprd.WithAppID(appID)),
		workflow.WithDaprdOptions(1, daprd.WithAppID(appID)),
	)
	return []framework.Option{framework.WithProcesses(r.wf)}
}

func (r *forcepurgeremote) Run(t *testing.T, ctx context.Context) {
	r.wf.WaitUntilRunning(t, ctx)
	addWorkflows(t, r.wf.Registry())

	const id = api.InstanceID("forcepurgeremote")
	startWith(t, ctx, r.wf, 0, r.staged, wfEvent, statusRunning, id)
	waitNoWorker(t, ctx, r.wf, 1, r.staged)
	mgmt := []*client.TaskHubGrpcClient{
		r.wf.ManagementClientN(t, ctx, 0),
		r.wf.ManagementClientN(t, ctx, 1),
	}

	// With the preview on, a staged raise loads the workflow actor on the
	// daprd which owns it. The event is not the one the workflow waits for.
	got := call(ctx, r.staged, func(ctx context.Context) error {
		return mgmt[0].RaiseEvent(ctx, id, "other")
	})
	assert.Equal(t, want(r.staged, noWorker, accepted), got)

	purger := 1
	if r.staged {
		owner := -1
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			for i := range 2 {
				if n, _ := r.wf.DaprN(i).ActiveActorCount(c, ctx, r.wf.WorkflowActorType(i)); n > 0 {
					owner = i
				}
			}
			assert.GreaterOrEqual(c, owner, 0)
		}, time.Second*10, time.Millisecond*50)
		purger = 1 - owner
	}

	got = call(ctx, r.staged, func(ctx context.Context) error {
		return mgmt[purger].PurgeWorkflowState(ctx, id, api.WithForcePurge(true))
	})
	assert.Equal(t, accepted, got)
	assert.Equal(t, statusNotFound, statusOf(ctx, mgmt[purger], id))

	got = call(ctx, r.staged, func(ctx context.Context) error {
		return mgmt[purger].RaiseEvent(ctx, id, evGo, api.WithEventPayload("after"))
	})
	assert.Equal(t, want(r.staged, noWorker, etagMismatch), got)
	assert.Equal(t, statusNotFound, statusOf(ctx, mgmt[purger], id))

	got = call(ctx, r.staged, func(ctx context.Context) error {
		return mgmt[purger].RaiseEvent(ctx, id, evGo, api.WithEventPayload("again"))
	})
	assert.Equal(t, want(r.staged, noWorker, notFound), got)
	assert.Equal(t, statusNotFound, statusOf(ctx, mgmt[purger], id))
}
