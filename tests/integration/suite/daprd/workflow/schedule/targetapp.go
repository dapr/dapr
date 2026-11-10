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

package schedule

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	wfschedule "github.com/dapr/dapr/pkg/runtime/wfengine/inprocess/schedule/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	wf "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(targetapp))
}

// targetapp checks a schedule starts its runs on the app it targets, named
// with its namespace, that Get reads the last run from there, and that
// same-named schedules of two apps targeting one app do not share run IDs.
type targetapp struct {
	workflow *workflow.Workflow
}

func (a *targetapp) Setup(t *testing.T) []framework.Option {
	a.workflow = workflow.New(t, workflow.WithDaprds(2))
	return []framework.Option{
		framework.WithProcesses(a.workflow),
	}
}

func (a *targetapp) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	a.workflow.RegistryN(1).AddWorkflowN("echo", func(*task.WorkflowContext) (any, error) { return "ran", nil })
	src := a.workflow.BackendClientN(t, ctx, 0)
	dst := a.workflow.BackendClientN(t, ctx, 1)
	dstApp := a.workflow.DaprN(1).AppID()
	srcRun := "shared_" + wfschedule.Owner(a.workflow.DaprN(0).Namespace(), a.workflow.DaprN(0).AppID()) + "_"
	dstRun := "shared_" + wfschedule.Owner(a.workflow.DaprN(1).Namespace(), dstApp) + "_"

	dstNamespace := a.workflow.DaprN(1).Namespace()
	_, err := src.CreateWorkflowSchedule(ctx, "shared", "echo", "@every 1s",
		api.WithScheduleTargetAppID(dstApp), api.WithScheduleTargetAppNamespace(dstNamespace))
	require.NoError(t, err)
	_, err = dst.CreateWorkflowSchedule(ctx, "shared", "echo", "@every 1s")
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		var fromSrc, fromDst int
		for _, id := range wf.ScheduleRuns(c, ctx, dst, "shared") {
			switch {
			case strings.HasPrefix(id, srcRun):
				fromSrc++
			case strings.HasPrefix(id, dstRun):
				fromDst++
			}
		}
		assert.GreaterOrEqual(c, fromSrc, 2)
		assert.GreaterOrEqual(c, fromDst, 2)
	}, 20*time.Second, 10*time.Millisecond)
	assert.Empty(t, wf.ScheduleRuns(t, ctx, src, "shared"), "runs start on the target app only")

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, gerr := src.GetWorkflowSchedule(ctx, "shared")
		if !assert.NoError(c, gerr) {
			return
		}
		ws := resp.GetSchedule()
		assert.Equal(c, dstApp, ws.GetTargetAppID())
		assert.Equal(c, dstNamespace, ws.GetTargetAppNamespace())
		run := ws.GetStatus().GetLastRun()
		assert.True(c, strings.HasPrefix(run.GetInstanceId(), srcRun), run.GetInstanceId())
		assert.Equal(c, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, run.GetRuntimeStatus())
		assert.JSONEq(c, `"ran"`, run.GetOutput().GetValue())
	}, 10*time.Second, 10*time.Millisecond)
}
