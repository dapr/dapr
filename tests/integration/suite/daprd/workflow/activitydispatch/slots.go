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
	suite.Register(new(slots))
}

// slots asserts that a per-sidecar slot count above one is honoured exactly:
// with two slots per replica and two replicas, six activities run as two on
// each replica with two waiting, and the waiting ones start as slots free.
type slots struct {
	workflow *workflow.Workflow
}

func (s *slots) Setup(t *testing.T) []framework.Option {
	const appID = "pull-slots"
	cfg := pullConfig("pullslots", 2)
	s.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, cfg), daprd.WithAppID(appID)),
		workflow.WithDaprdOptions(1, daprd.WithConfigManifests(t, cfg), daprd.WithAppID(appID)),
	)
	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *slots) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	var running [2]atomic.Int64
	var total atomic.Int64
	releaseCh := make(chan struct{})
	for i := range 2 {
		s.workflow.RegistryN(i).AddWorkflowN("fanout", func(ctx *task.WorkflowContext) (any, error) {
			tasks := make([]task.Task, 6)
			for j := range tasks {
				tasks[j] = ctx.CallActivity("slow")
			}
			for _, tk := range tasks {
				if err := tk.Await(nil); err != nil {
					return nil, err
				}
			}
			return nil, nil
		})
		s.workflow.RegistryN(i).AddActivityN("slow", func(ctx task.ActivityContext) (any, error) {
			running[i].Add(1)
			total.Add(1)
			<-releaseCh
			running[i].Add(-1)
			return nil, nil
		})
	}

	client := s.workflow.BackendClientN(t, ctx, 0)
	s.workflow.BackendClientN(t, ctx, 1)

	id, err := client.ScheduleNewWorkflow(ctx, "fanout", api.WithStartTime(time.Now()))
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(2), running[0].Load())
		assert.Equal(c, int64(2), running[1].Load())
	}, time.Second*20, time.Millisecond*10)
	time.Sleep(time.Second)
	assert.Equal(t, int64(4), total.Load(), "no replica runs more than its two slots")

	releaseCh <- struct{}{}
	releaseCh <- struct{}{}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(6), total.Load())
		assert.Equal(c, int64(4), running[0].Load()+running[1].Load())
	}, time.Second*20, time.Millisecond*10)

	close(releaseCh)
	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
}
