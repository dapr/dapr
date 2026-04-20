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

package globalmaxconcurrent

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
	suite.Register(new(nolimit))
}

// nolimit tests backward compatibility: when no global concurrency limits are
// configured, all activities run without restriction.
type nolimit struct {
	workflow *workflow.Workflow
}

func (n *nolimit) Setup(t *testing.T) []framework.Option {
	const appID = "globalmax-nolimit"
	n.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, daprd.WithAppID(appID)),
		workflow.WithDaprdOptions(1, daprd.WithAppID(appID)),
	)

	return []framework.Option{
		framework.WithProcesses(n.workflow),
	}
}

func (n *nolimit) Run(t *testing.T, ctx context.Context) {
	n.workflow.WaitUntilRunning(t, ctx)

	var inside atomic.Int64
	doneCh := make(chan struct{})

	for i := range 2 {
		n.workflow.RegistryN(i).AddWorkflowN("nolimit", func(ctx *task.WorkflowContext) (any, error) {
			ids := make([]task.Task, 5)
			for j := range ids {
				ids[j] = ctx.CallActivity("work")
			}
			for _, id := range ids {
				require.NoError(t, id.Await(nil))
			}
			return nil, nil
		})
		n.workflow.RegistryN(i).AddActivityN("work", func(ctx task.ActivityContext) (any, error) {
			inside.Add(1)
			<-doneCh
			return nil, nil
		})
	}

	client0 := n.workflow.BackendClientN(t, ctx, 0)
	client1 := n.workflow.BackendClientN(t, ctx, 1)

	// Schedule workflows on both instances. 2 workflows x 5 activities = 10.
	_, err := client0.ScheduleNewWorkflow(ctx, "nolimit", api.WithStartTime(time.Now()))
	require.NoError(t, err)
	_, err = client1.ScheduleNewWorkflow(ctx, "nolimit", api.WithStartTime(time.Now()))
	require.NoError(t, err)

	// All 10 activities should start without any throttling.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(10), inside.Load())
	}, time.Second*20, time.Millisecond*10)

	close(doneCh)
}
