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

package workflow

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(terminatecompleted))
}

type terminatecompleted struct {
	workflow *workflow.Workflow
}

func (e *terminatecompleted) Setup(t *testing.T) []framework.Option {
	e.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(e.workflow),
	}
}

func (e *terminatecompleted) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	e.workflow.Registry(0).AddOrchestratorN("foo", func(ctx *task.OrchestrationContext) (any, error) {
		require.NoError(t, ctx.CallActivity("bar").Await(nil))
		return nil, nil
	})
	e.workflow.Registry(0).AddActivityN("bar", func(ctx task.ActivityContext) (any, error) {
		return nil, nil
	})

	cl := e.workflow.BackendClient(t, ctx, 0)
	id, err := cl.ScheduleNewOrchestration(ctx, "foo")
	require.NoError(t, err)
	_, err = cl.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)
	require.NoError(t, cl.TerminateOrchestration(ctx, id))

	//nolint:staticcheck
	_, err = e.workflow.Dapr().GRPCClient(t, ctx).TerminateWorkflowAlpha1(ctx, &rtv1.TerminateWorkflowRequest{
		InstanceId:        string(id),
		WorkflowComponent: "dapr",
	})
	require.NoError(t, err)

	rurl := fmt.Sprintf("http://%s/v1.0-beta1/workflows/dapr/%s/terminate", e.workflow.Dapr().HTTPAddress(), id)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rurl, nil)
	require.NoError(t, err)
	resp, err := client.HTTP(t).Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusAccepted, resp.StatusCode)
	require.NoError(t, resp.Body.Close())
}
