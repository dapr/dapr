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

package loadbalance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(stalecompletion))
}

// stalecompletion re-delivers a turn completion while a later turn is in
// flight and requires the workflow to converge on the genuine response.
type stalecompletion struct {
	workflow *workflow.Workflow
}

func (s *stalecompletion) Setup(t *testing.T) []framework.Option {
	config := `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
    name: clusteredfastpath
spec:
    features:
    - name: WorkflowsClusteredDeployment
      enabled: true
    - name: WorkflowsLocalWakeFastPath
      enabled: true
    - name: WorkflowsLocalActivityFastPath
      enabled: true
    - name: WorkflowsCompletionsFold
      enabled: true
`
	uid, err := uuid.NewRandom()
	require.NoError(t, err)

	s.workflow = workflow.New(t,
		workflow.WithDaprds(1),
		workflow.WithDaprdOptions(0,
			daprd.WithAppID(uid.String()),
			daprd.WithConfigManifests(t, config),
			daprd.WithExecOptions(exec.WithEnvVars(t,
				"DAPR_WORKFLOW_JANITOR_PERIOD", "2s",
				"DAPR_WORKFLOW_TEST_DUPLICATE_TURN_COMPLETIONS", "3",
			)),
		),
	)

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *stalecompletion) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	registry := task.NewTaskRegistry()
	require.NoError(t, registry.AddWorkflowN("Seq", func(c *task.WorkflowContext) (any, error) {
		var out string
		for range 5 {
			if err := c.CallActivity("Echo", task.WithActivityInput("x")).Await(&out); err != nil {
				return nil, err
			}
		}
		return out, nil
	}))
	require.NoError(t, registry.AddActivityN("Echo", func(task.ActivityContext) (any, error) {
		time.Sleep(time.Millisecond * 250)
		return "ok", nil
	}))

	cl := client.NewTaskHubGrpcClient(s.workflow.DaprN(0).GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, cl.StartWorkItemListener(ctx, registry))

	resp, err := s.workflow.DaprN(0).GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "Seq",
		InstanceId:        uuid.New().String(),
	})
	require.NoError(t, err)

	wctx, cancel := context.WithTimeout(ctx, time.Second*60)
	defer cancel()
	metadata, err := cl.WaitForWorkflowCompletion(wctx, api.InstanceID(resp.GetInstanceId()))
	require.NoError(t, err, "workflow stranded after a duplicate turn-completion delivery (janitor-livelock: stale response adopted across turns)")
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))
	assert.Equal(t, `"ok"`, metadata.GetOutput().GetValue())
}
