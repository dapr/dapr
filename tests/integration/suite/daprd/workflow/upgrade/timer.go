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
	suite.Register(new(timer))
}

// timer kills the release daprd while a workflow timer is pending and
// expects master to fire the timer reminder and complete the workflow.
type timer struct {
	upgrade *wfupgrade.Upgrade
}

func (m *timer) Setup(t *testing.T) []framework.Option {
	m.upgrade = wfupgrade.New(t)

	return []framework.Option{
		framework.WithProcesses(m.upgrade),
	}
}

func (m *timer) Run(t *testing.T, ctx context.Context) {
	reg := task.NewTaskRegistry()
	require.NoError(t, reg.AddWorkflowN("timer", func(c *task.WorkflowContext) (any, error) {
		return "fired", c.CreateTimer(2 * time.Second).Await(nil)
	}))

	from := m.upgrade.Start(t, ctx, m.upgrade.From(), reg)
	id, err := from.ScheduleNewWorkflow(ctx, "timer", api.WithInstanceID("timer"))
	require.NoError(t, err)
	m.upgrade.Scheduler().WaitJobKeyCount(t, ctx, "timer-", func(n int) bool { return n > 0 })

	m.upgrade.From().Kill(t)

	to := m.upgrade.Start(t, ctx, m.upgrade.To(), reg)
	meta, err := to.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.Equal(t, `"fired"`, meta.GetOutput().GetValue())
}
