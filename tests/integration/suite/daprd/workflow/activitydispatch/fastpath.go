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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(fastpath))
}

// fastpath asserts that enabling pull dispatch turns the WorkflowsFastPath
// preview off for the app, with a log line saying so, and that workflows still
// complete through the scheduler path.
type fastpath struct {
	workflow *workflow.Workflow
	logline  *logline.LogLine
}

func (f *fastpath) Setup(t *testing.T) []framework.Option {
	f.logline = logline.New(t,
		logline.WithStdoutLineContains(
			"WorkflowsFastPath is enabled but pull activity dispatch is configured; disabling the fast path",
		),
	)

	f.workflow = workflow.New(t,
		workflow.WithFastPath(true),
		workflow.WithDaprdOptions(0,
			daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: pullfastpath
spec:
  workflow:
    maxConcurrentActivityInvocations: 2
    activityDispatchMode: pull
`),
			daprd.WithLogLineStdout(f.logline),
		),
	)

	return []framework.Option{
		framework.WithProcesses(f.logline, f.workflow),
	}
}

func (f *fastpath) Run(t *testing.T, ctx context.Context) {
	f.workflow.WaitUntilRunning(t, ctx)
	f.logline.EventuallyFoundAll(t)

	f.workflow.Registry().AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallActivity("act", task.WithActivityInput("in")).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	})
	f.workflow.Registry().AddActivityN("act", func(ctx task.ActivityContext) (any, error) {
		var in string
		if err := ctx.GetInput(&in); err != nil {
			return nil, err
		}
		return in + "-done", nil
	})

	client := f.workflow.BackendClient(t, ctx)
	id, err := client.ScheduleNewWorkflow(ctx, "wf", api.WithStartTime(time.Now()))
	require.NoError(t, err)
	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, `"in-done"`, meta.GetOutput().GetValue())
}
