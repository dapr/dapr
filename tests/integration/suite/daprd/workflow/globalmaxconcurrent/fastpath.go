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
	suite.Register(new(fastpath))
}

type fastpath struct {
	workflow *workflow.Workflow
}

func (f *fastpath) Setup(t *testing.T) []framework.Option {
	configManifest := `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: globalmax
spec:
  features:
  - name: WorkflowsFastPath
    enabled: true
  workflow:
    globalMaxConcurrentWorkflowInvocations: 2
`
	const appID = "globalmax-fastpath"
	f.workflow = workflow.New(t,
		workflow.WithDaprds(3),
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, configManifest), daprd.WithAppID(appID)),
		workflow.WithDaprdOptions(1, daprd.WithConfigManifests(t, configManifest), daprd.WithAppID(appID)),
		workflow.WithDaprdOptions(2, daprd.WithConfigManifests(t, configManifest), daprd.WithAppID(appID)),
	)

	return []framework.Option{
		framework.WithProcesses(f.workflow),
	}
}

func (f *fastpath) Run(t *testing.T, ctx context.Context) {
	f.workflow.WaitUntilRunning(t, ctx)

	var inside atomic.Int64
	doneCh := make(chan struct{})
	defer close(doneCh)

	for idx := range 3 {
		f.workflow.RegistryN(idx).AddWorkflowN("globalmax", func(ctx *task.WorkflowContext) (any, error) {
			inside.Add(1)
			<-doneCh
			return nil, nil
		})
	}

	client0 := f.workflow.BackendClientN(t, ctx, 0)
	client1 := f.workflow.BackendClientN(t, ctx, 1)
	client2 := f.workflow.BackendClientN(t, ctx, 2)

	_, err := client0.ScheduleNewWorkflow(ctx, "globalmax", api.WithStartTime(time.Now()))
	require.NoError(t, err)
	_, err = client1.ScheduleNewWorkflow(ctx, "globalmax", api.WithStartTime(time.Now()))
	require.NoError(t, err)
	_, err = client2.ScheduleNewWorkflow(ctx, "globalmax", api.WithStartTime(time.Now()))
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(2), inside.Load())
	}, time.Second*10, time.Millisecond*10)

	time.Sleep(time.Second * 2)
	assert.Equal(t, int64(2), inside.Load(), "the concurrency cap must hold with WorkflowsFastPath enabled")

	doneCh <- struct{}{}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(3), inside.Load())
	}, time.Second*10, time.Millisecond*10)
}
