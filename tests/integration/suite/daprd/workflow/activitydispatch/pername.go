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
	suite.Register(new(pername))
}

// pername asserts that a per-name dispatchMode override composes with the
// per-name global limit on the same entry: Transcode is pull-dispatched into
// the fleet's slots but never runs more than its global maxConcurrent, while
// an activity without an override stays on hashed dispatch and completes.
type pername struct {
	workflow *workflow.Workflow
}

func (p *pername) Setup(t *testing.T) []framework.Option {
	const appID = "pull-pername"
	configManifest := `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: pullpername
spec:
  workflow:
    maxConcurrentActivityInvocations: 2
    activityConcurrencyLimits:
      - name: Transcode
        maxConcurrent: 1
        dispatchMode: pull
`
	p.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, configManifest), daprd.WithAppID(appID)),
		workflow.WithDaprdOptions(1, daprd.WithConfigManifests(t, configManifest), daprd.WithAppID(appID)),
	)

	return []framework.Option{
		framework.WithProcesses(p.workflow),
	}
}

func (p *pername) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	var transcoding atomic.Int64
	var cheap atomic.Int64
	releaseCh := make(chan struct{})

	for i := range 2 {
		p.workflow.RegistryN(i).AddWorkflowN("mixed", func(ctx *task.WorkflowContext) (any, error) {
			tasks := []task.Task{
				ctx.CallActivity("Transcode"),
				ctx.CallActivity("Transcode"),
				ctx.CallActivity("Transcode"),
				ctx.CallActivity("Cheap"),
			}
			for _, tk := range tasks {
				if err := tk.Await(nil); err != nil {
					return nil, err
				}
			}
			return nil, nil
		})
		p.workflow.RegistryN(i).AddActivityN("Transcode", func(ctx task.ActivityContext) (any, error) {
			transcoding.Add(1)
			<-releaseCh
			return nil, nil
		})
		p.workflow.RegistryN(i).AddActivityN("Cheap", func(ctx task.ActivityContext) (any, error) {
			cheap.Add(1)
			return nil, nil
		})
	}

	client := p.workflow.BackendClientN(t, ctx, 0)
	p.workflow.BackendClientN(t, ctx, 1)
	id, err := client.ScheduleNewWorkflow(ctx, "mixed", api.WithStartTime(time.Now()))
	require.NoError(t, err)

	// Four fleet slots, but the per-name global limit admits one Transcode.
	// Cheap is hashed and unaffected by the Transcode gate.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), transcoding.Load())
		assert.Equal(c, int64(1), cheap.Load())
	}, time.Second*20, time.Millisecond*10)

	time.Sleep(time.Second)
	assert.Equal(t, int64(1), transcoding.Load())

	releaseCh <- struct{}{}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(2), transcoding.Load())
	}, time.Second*20, time.Millisecond*10)

	releaseCh <- struct{}{}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(3), transcoding.Load())
	}, time.Second*20, time.Millisecond*10)

	close(releaseCh)
	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
}
