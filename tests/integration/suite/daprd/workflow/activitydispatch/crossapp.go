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

package activitydispatch

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(crossapp))
}

// crossapp asserts that for an activity scheduled in another application the
// target application's Configuration decides the dispatch mode: the
// orchestrator app runs the default hashed mode, the worker app runs pull, and
// the worker replicas each take one activity.
type crossapp struct {
	workflow *workflow.Workflow
}

func (c *crossapp) Setup(t *testing.T) []framework.Option {
	const workerAppID = "pull-crossapp-worker"
	cfg := pullConfig("pullcrossappworker", 1)
	c.workflow = workflow.New(t,
		workflow.WithDaprds(3),
		workflow.WithDaprdOptions(0, daprd.WithAppID("pull-crossapp-orchestrator")),
		workflow.WithDaprdOptions(1, daprd.WithConfigManifests(t, cfg), daprd.WithAppID(workerAppID)),
		workflow.WithDaprdOptions(2, daprd.WithConfigManifests(t, cfg), daprd.WithAppID(workerAppID)),
	)
	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *crossapp) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	workerAppID := c.workflow.DaprN(1).AppID()
	var started [3]atomic.Int64
	releaseCh := make(chan struct{})

	c.workflow.RegistryN(0).AddWorkflowN("remote", func(ctx *task.WorkflowContext) (any, error) {
		t1 := ctx.CallActivity("slow", task.WithActivityAppID(workerAppID))
		t2 := ctx.CallActivity("slow", task.WithActivityAppID(workerAppID))
		if err := t1.Await(nil); err != nil {
			return nil, err
		}
		return nil, t2.Await(nil)
	})
	for i := 1; i <= 2; i++ {
		c.workflow.RegistryN(i).AddActivityN("slow", func(ctx task.ActivityContext) (any, error) {
			started[i].Add(1)
			<-releaseCh
			return nil, nil
		})
	}

	client := c.workflow.BackendClientN(t, ctx, 0)
	c.workflow.BackendClientN(t, ctx, 1)
	c.workflow.BackendClientN(t, ctx, 2)

	id, err := client.ScheduleNewWorkflow(ctx, "remote", api.WithStartTime(time.Now()))
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.Equal(col, int64(1), started[1].Load())
		assert.Equal(col, int64(1), started[2].Load())
	}, time.Second*20, time.Millisecond*10)
	assert.Equal(t, int64(0), started[0].Load())

	close(releaseCh)
	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
}
