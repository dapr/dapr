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
	suite.Register(new(externalevent))
}

// externalevent kills the release daprd while a workflow waits for an
// external event and raises the event through master, which must replay the
// release's history and complete the workflow.
type externalevent struct {
	upgrade *wfupgrade.Upgrade
}

func (e *externalevent) Setup(t *testing.T) []framework.Option {
	e.upgrade = wfupgrade.New(t)

	return []framework.Option{
		framework.WithProcesses(e.upgrade),
	}
}

func (e *externalevent) Run(t *testing.T, ctx context.Context) {
	reg := task.NewTaskRegistry()
	require.NoError(t, reg.AddWorkflowN("event", func(c *task.WorkflowContext) (any, error) {
		var payload string
		err := c.WaitForSingleEvent("go", time.Hour).Await(&payload)
		return payload, err
	}))

	from := e.upgrade.Start(t, ctx, e.upgrade.From(), reg)
	id, err := from.ScheduleNewWorkflow(ctx, "event", api.WithInstanceID("event"))
	require.NoError(t, err)
	_, err = from.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	e.upgrade.From().Kill(t)

	to := e.upgrade.Start(t, ctx, e.upgrade.To(), reg)
	require.NoError(t, to.RaiseEvent(ctx, id, "go", api.WithEventPayload("payload")))
	meta, err := to.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.Equal(t, `"payload"`, meta.GetOutput().GetValue())
}
