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

package upgrade

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	wfupgrade "github.com/dapr/dapr/tests/integration/framework/process/workflow/upgrade"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(activity))
}

// activity kills the release daprd while an activity is executing and
// expects master to re-fire the durable run-activity reminder and complete
// the workflow.
type activity struct {
	upgrade *wfupgrade.Upgrade
}

func (a *activity) Setup(t *testing.T) []framework.Option {
	a.upgrade = wfupgrade.New(t)

	return []framework.Option{
		framework.WithProcesses(a.upgrade),
	}
}

func (a *activity) Run(t *testing.T, ctx context.Context) {
	var entries atomic.Int64
	reg := task.NewTaskRegistry()
	require.NoError(t, reg.AddWorkflowN("echo", func(c *task.WorkflowContext) (any, error) {
		var out string
		if err := c.CallActivity("echo", task.WithActivityInput("dapr")).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	require.NoError(t, reg.AddActivityN("echo", func(c task.ActivityContext) (any, error) {
		var in string
		if err := c.GetInput(&in); err != nil {
			return nil, err
		}
		if entries.Add(1) == 1 {
			<-c.Context().Done()
			return nil, c.Context().Err()
		}
		return in + " survived", nil
	}))

	from := a.upgrade.Start(t, ctx, a.upgrade.From(), reg)
	id, err := from.ScheduleNewWorkflow(ctx, "echo", api.WithInstanceID("activity"))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return entries.Load() == 1
	}, time.Second*20, time.Millisecond*10)

	a.upgrade.From().Kill(t)

	to := a.upgrade.Start(t, ctx, a.upgrade.To(), reg)
	meta, err := to.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.Equal(t, `"dapr survived"`, meta.GetOutput().GetValue())
	assert.Equal(t, int64(2), entries.Load())
}
