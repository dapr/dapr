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

package disk

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(midrun))
}

type midrun struct {
	wf     *workflow.Workflow
	stepCh chan struct{}
}

func (m *midrun) Setup(t *testing.T) []framework.Option {
	m.stepCh = make(chan struct{}, 10)
	m.wf = workflow.New(t,
		workflow.WithSchedulerOptions(scheduler.WithEtcdSpaceQuota("16Mi")),
		workflow.WithAddActivity(t, "step", func(ctx task.ActivityContext) (any, error) {
			select {
			case m.stepCh <- struct{}{}:
			default:
			}
			return nil, nil
		}),
		workflow.WithAddOrchestrator(t, "multiTimerFlow", func(ctx *task.WorkflowContext) (any, error) {
			for range 5 {
				if err := ctx.CreateTimer(time.Second).Await(nil); err != nil {
					return nil, err
				}
				if err := ctx.CallActivity("step").Await(nil); err != nil {
					return nil, err
				}
			}
			return nil, nil
		}),
	)
	return []framework.Option{framework.WithProcesses(m.wf)}
}

func (m *midrun) Run(t *testing.T, ctx context.Context) {
	m.wf.WaitUntilRunning(t, ctx)

	sched := m.wf.Scheduler()
	cl := m.wf.BackendClient(t, ctx)

	id, err := cl.ScheduleNewWorkflow(ctx, "multiTimerFlow",
		api.WithInstanceID("wf-midrun"))
	require.NoError(t, err)

	stepCtx, cancelStep := context.WithTimeout(ctx, 10*time.Second)
	defer cancelStep()
	select {
	case <-m.stepCh:
	case <-stepCtx.Done():
		t.Fatalf("workflow did not progress past first step: %v", stepCtx.Err())
	}

	sched.FillQuota(t, ctx, "quota-mid/")

	httpClient := client.HTTP(t)
	healthzURL := fmt.Sprintf("http://127.0.0.1:%d/healthz", sched.HealthzPort())
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthzURL, nil)
		if !assert.NoError(c, err) {
			return
		}
		resp, err := httpClient.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		if err != nil {
			return
		}
		assert.NotEqual(c, http.StatusOK, resp.StatusCode)
	}, 15*time.Second, 50*time.Millisecond)

	sched.Kill(t)

	waitCtx, cancelWait := context.WithTimeout(ctx, 3*time.Second)
	defer cancelWait()
	_, waitErr := cl.WaitForWorkflowCompletion(waitCtx, id)
	require.Error(t, waitErr, "workflow must not complete while scheduler is dead")
}
