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

package hotreload

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(sighup))
}

type sighup struct {
	workflow   *workflow.Workflow
	configFile string
}

func (s *sighup) Setup(t *testing.T) []framework.Option {
	s.configFile = os.WriteFileYaml(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: wfsighup
spec:
  metrics:
    http:
      increasedCardinality: false
`)

	s.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithConfigs(s.configFile)),
		workflow.WithAddActivityN(t, 0, "SayHello", func(ctx task.ActivityContext) (any, error) {
			var name string
			if err := ctx.GetInput(&name); err != nil {
				return nil, err
			}
			return fmt.Sprintf("Hello, %s!", name), nil
		}),
		workflow.WithAddWorkflowN(t, 0, "SingleActivity", func(ctx *task.WorkflowContext) (any, error) {
			var input string
			if err := ctx.GetInput(&input); err != nil {
				return nil, err
			}
			var output string
			err := ctx.CallActivity("SayHello", task.WithActivityInput(input)).Await(&output)
			return output, err
		}),
	)

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *sighup) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	client := s.workflow.BackendClient(t, ctx)
	id, err := client.ScheduleNewWorkflow(ctx, "SingleActivity", api.WithInput("Dapr"))
	require.NoError(t, err)
	meta, err := client.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(meta))
	assert.Equal(t, `"Hello, Dapr!"`, meta.GetOutput().GetValue())

	os.WriteFileTo(t, s.configFile, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: wfsighup
spec:
  metrics:
    http:
      increasedCardinality: true
`)
	s.workflow.Dapr().SignalHUP(t)

	s.workflow.WaitUntilRunning(t, ctx)

	client = s.workflow.BackendClient(t, ctx)
	id, err = client.ScheduleNewWorkflow(ctx, "SingleActivity", api.WithInput("Dapr"))
	require.NoError(t, err)
	meta, err = client.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(meta))
	assert.Equal(t, `"Hello, Dapr!"`, meta.GetOutput().GetValue())
}
