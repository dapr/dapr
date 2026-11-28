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

package durable

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
)

func init() {
	suite.Register(new(kill))
}

// kill stops daprd while the save at the end of a workflow turn is held,
// and requires the worker's CompleteWorkflowTask call not to have been
// answered as a success: the turn was never saved.
type kill struct {
	workflow *workflow.Workflow
}

func (k *kill) Setup(t *testing.T) []framework.Option {
	k.workflow = workflow.New(t,
		workflow.WithClusteredDeployment(false),
		workflow.WithDaprdOptions(0, daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_WORKFLOW_TEST_TURN_SAVE_HOLD", "1h"))),
	)

	return []framework.Option{
		framework.WithProcesses(k.workflow),
	}
}

func (k *kill) Run(t *testing.T, ctx context.Context) {
	k.workflow.WaitUntilRunning(t, ctx)
	worker := k.workflow.ConnectRawWorker(t, ctx)
	cl := k.workflow.ManagementClient(t, ctx)

	const id = api.InstanceID("durable-kill")
	// The start is answered once the first turn is saved, which never
	// happens on this daprd, so its answer is not checked.
	go cl.ScheduleNewWorkflow(ctx, string(id), api.WithInstanceID(id)) //nolint:errcheck

	resp := workflow.CompletedResponse(worker.NextWorkflowItem(t))
	errCh := make(chan error, 1)
	go func() {
		_, err := worker.Client().CompleteWorkflowTask(ctx, resp)
		errCh <- err
	}()

	select {
	case err := <-errCh:
		require.FailNow(t, "the completion was answered while its save was held", "err: %v", err)
	case <-time.After(time.Second):
	}

	k.workflow.Dapr().Kill(t)
	select {
	case err := <-errCh:
		require.Error(t, err, "a turn that was never saved must not be answered as a success")
		assert.Equal(t, codes.Unavailable, status.Code(err))
	case <-time.After(10 * time.Second):
		require.FailNow(t, "the completion call did not end when daprd stopped")
	}

	k.workflow.Dapr().Restart(t, ctx)
	k.workflow.Dapr().WaitUntilRunning(t, ctx)
	meta, err := k.workflow.ManagementClient(t, ctx).FetchWorkflowMetadata(ctx, id)
	require.NoError(t, err)
	assert.NotEqual(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())
}
