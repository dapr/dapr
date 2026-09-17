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
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(redeliver))
}

// redeliver asserts at-least-once under pull dispatch: when the sidecar
// running a pull activity dies, the scheduler redelivers the activity to the
// surviving sidecar and the workflow completes there.
type redeliver struct {
	workflow *workflow.Workflow
}

func (r *redeliver) Setup(t *testing.T) []framework.Option {
	const appID = "pull-redeliver"
	configManifest := `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: pullredeliver
spec:
  workflow:
    maxConcurrentActivityInvocations: 1
    activityDispatchMode: pull
`
	r.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, configManifest), daprd.WithAppID(appID)),
		workflow.WithDaprdOptions(1, daprd.WithConfigManifests(t, configManifest), daprd.WithAppID(appID)),
	)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *redeliver) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	var started [2]atomic.Int64
	var release [2]chan struct{}
	for i := range 2 {
		release[i] = make(chan struct{})
		r.workflow.RegistryN(i).AddWorkflowN("single", func(ctx *task.WorkflowContext) (any, error) {
			return nil, ctx.CallActivity("slow").Await(nil)
		})
		r.workflow.RegistryN(i).AddActivityN("slow", func(ctx task.ActivityContext) (any, error) {
			started[i].Add(1)
			<-release[i]
			return nil, nil
		})
	}

	// A client per daprd so the wait below can go through the survivor.
	clients := []*client.TaskHubGrpcClient{
		r.workflow.BackendClientN(t, ctx, 0),
		r.workflow.BackendClientN(t, ctx, 1),
	}

	id, err := clients[0].ScheduleNewWorkflow(ctx, "single", api.WithStartTime(time.Now()))
	require.NoError(t, err)

	var victim int
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		total := started[0].Load() + started[1].Load()
		if !assert.Equal(c, int64(1), total) {
			return
		}
		if started[1].Load() == 1 {
			victim = 1
		}
	}, time.Second*20, time.Millisecond*10)
	survivor := 1 - victim

	r.workflow.DaprN(victim).Kill(t)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), started[survivor].Load(), "the scheduler must redeliver the lost activity to the surviving sidecar")
	}, time.Second*60, time.Millisecond*10)

	close(release[survivor])
	_, err = clients[survivor].WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
}
