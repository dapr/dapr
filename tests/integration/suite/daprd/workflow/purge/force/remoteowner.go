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

package force

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
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(remoteowner))
}

// remoteowner force purges a running workflow through the daprd that does not
// host its actor. The force purge runs where it was received, so the daprd
// that hosts the actor is asked to evict it: the actor leaves that daprd long
// before the idle reaper would take it, and the next call to the instance
// finds it gone instead of being judged on the purged instance's cache.
type remoteowner struct {
	workflow *workflow.Workflow
}

func (r *remoteowner) Setup(t *testing.T) []framework.Option {
	appID := uuid.NewString()
	r.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, daprd.WithAppID(appID)),
		workflow.WithDaprdOptions(1, daprd.WithAppID(appID)),
	)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *remoteowner) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("wait", func(ctx *dworkflow.WorkflowContext) (any, error) {
		return nil, ctx.WaitForExternalEvent("go", time.Hour).Await(nil)
	})

	clients := []*dworkflow.Client{
		r.workflow.WorkflowClientN(t, ctx, 0),
		r.workflow.WorkflowClientN(t, ctx, 1),
	}
	for _, c := range clients {
		require.NoError(t, c.StartWorker(ctx, reg))
	}

	const id = "remoteowner"
	_, err := clients[0].ScheduleWorkflow(ctx, "wait", dworkflow.WithInstanceID(id))
	require.NoError(t, err)
	_, err = clients[0].WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	actorType := r.workflow.WorkflowActorType(0)
	active := func(c assert.TestingT, i int) int {
		n, _ := r.workflow.DaprN(i).ActiveActorCount(c, ctx, actorType)
		return n
	}

	owner := -1
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		for i := range 2 {
			if active(c, i) > 0 {
				owner = i
			}
		}
		assert.GreaterOrEqual(c, owner, 0)
	}, time.Second*10, time.Millisecond*50)
	purger := 1 - owner

	require.NoError(t, clients[purger].PurgeWorkflowState(ctx, id, dworkflow.WithForcePurge(true)))

	// The idle reaper takes an actor only after a minute of inactivity, so an
	// actor gone within seconds was evicted by the purge.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Zero(c, active(c, owner))
	}, time.Second*10, time.Millisecond*50)

	err = clients[owner].RaiseEvent(ctx, id, "go")
	require.Error(t, err)
	assert.Contains(t, err.Error(), api.ErrInstanceNotFound.Error())

	// The instance ID is free again on the daprd that held it.
	_, err = clients[owner].ScheduleWorkflow(ctx, "wait", dworkflow.WithInstanceID(id))
	require.NoError(t, err)
	_, err = clients[owner].WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)
}
