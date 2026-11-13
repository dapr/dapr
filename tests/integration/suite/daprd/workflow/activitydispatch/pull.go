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
	suite.Register(new(pull))
}

// pull asserts that with activityDispatchMode pull and one slot per sidecar,
// a fan-out of three activities lands on three different replicas: one each,
// which consistent hashing does not guarantee.
type pull struct {
	workflow *workflow.Workflow
}

func (p *pull) Setup(t *testing.T) []framework.Option {
	const appID = "pull-dispatch"
	configManifest := `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: pulldispatch
spec:
  workflow:
    maxConcurrentActivityInvocations: 1
    activityDispatchMode: pull
`
	p.workflow = workflow.New(t,
		workflow.WithDaprds(3),
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, configManifest), daprd.WithAppID(appID)),
		workflow.WithDaprdOptions(1, daprd.WithConfigManifests(t, configManifest), daprd.WithAppID(appID)),
		workflow.WithDaprdOptions(2, daprd.WithConfigManifests(t, configManifest), daprd.WithAppID(appID)),
	)

	return []framework.Option{
		framework.WithProcesses(p.workflow),
	}
}

func (p *pull) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	const n = 3
	var perDaprd [n]atomic.Int64
	releaseCh := make(chan struct{})

	for i := range n {
		p.workflow.RegistryN(i).AddWorkflowN("fanout", func(ctx *task.WorkflowContext) (any, error) {
			tasks := make([]task.Task, n)
			for j := range n {
				tasks[j] = ctx.CallActivity("slow")
			}
			for _, tk := range tasks {
				if err := tk.Await(nil); err != nil {
					return nil, err
				}
			}
			return nil, nil
		})
		p.workflow.RegistryN(i).AddActivityN("slow", func(ctx task.ActivityContext) (any, error) {
			perDaprd[i].Add(1)
			<-releaseCh
			return nil, nil
		})
	}

	// A worker per daprd, so every replica hosts the activity actor type.
	client := p.workflow.BackendClientN(t, ctx, 0)
	for i := 1; i < n; i++ {
		p.workflow.BackendClientN(t, ctx, i)
	}
	id, err := client.ScheduleNewWorkflow(ctx, "fanout", api.WithStartTime(time.Now()))
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		for i := range n {
			assert.Equal(c, int64(1), perDaprd[i].Load(), "daprd %d", i)
		}
	}, time.Second*20, time.Millisecond*10)

	// Hold for a moment to prove nothing else is dispatched onto a full slot.
	time.Sleep(time.Second)
	for i := range n {
		assert.Equal(t, int64(1), perDaprd[i].Load(), "daprd %d", i)
	}

	close(releaseCh)
	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
}
