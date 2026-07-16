/*
Copyright 2025 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://wwb.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package reconnect

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(executor))
}

type executor struct {
	workflow *workflow.Workflow
}

func (e *executor) Setup(t *testing.T) []framework.Option {
	config := `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
    name: workflowsclustereddeployment
spec:
    features:
    - name: WorkflowsClusteredDeployment
      enabled: true
`
	e.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0,
			daprd.WithConfigManifests(t, config),
		),
	)
	return []framework.Option{
		framework.WithProcesses(e.workflow),
	}
}

func (e *executor) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	cctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)

	client := e.workflow.WorkflowClient(t, cctx)
	client.StartWorker(cctx, dworkflow.NewRegistry())

	t.Log("Scheduling workflow")
	id, err := client.ScheduleWorkflow(ctx, "foo")
	require.NoError(t, err)

	t.Log("Waiting for workflow to start")
	_, err = client.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	t.Logf("restarting worker")
	cancel()
	client = e.workflow.WorkflowClient(t, ctx)
	client.StartWorker(ctx, dworkflow.NewRegistry())

	t.Log("Waiting again for workflow to start")
	_, err = client.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	t.Log("Raising event")
	require.NoError(t, client.RaiseEvent(ctx, id, "bar"))

	t.Log("Waiting for workflow to complete")
	tctx, tcancel := context.WithTimeout(ctx, 5*time.Second)
	defer tcancel()
	_, err = client.WaitForWorkflowCompletion(tctx, id)
	require.NoError(t, err)
}
