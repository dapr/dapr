/*
Copyright 2024 The Dapr Authors
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

package backend

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/microsoft/durabletask-go/api"
	"github.com/microsoft/durabletask-go/backend"
	"github.com/microsoft/durabletask-go/client"
	"github.com/microsoft/durabletask-go/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(sqlite))
}

type sqlite struct {
	daprd *daprd.Daprd
	dir   string
}

func (s *sqlite) Setup(t *testing.T) []framework.Option {
	s.dir = filepath.Join(t.TempDir(), "wf.db")
	s.daprd = daprd.New(t,
		daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: wfbackend
spec:
 type: workflowbackend.sqlite
 version: v1
 metadata:
 - name: connectionString
   value: %s
`, s.dir)),
	)

	return []framework.Option{
		framework.WithProcesses(s.daprd),
	}
}

func (s *sqlite) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)

	comps := util.GetMetaComponents(t, ctx, util.HTTPClient(t), s.daprd.HTTPPort())
	require.ElementsMatch(t, []*rtv1.RegisteredComponents{
		{Name: "wfbackend", Type: "workflowbackend.sqlite", Version: "v1"},
	}, comps)

	r := task.NewTaskRegistry()
	r.AddOrchestratorN("SingleActivity", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		var output string
		err := ctx.CallActivity("SayHello", task.WithActivityInput(input)).Await(&output)
		return output, err
	})
	r.AddActivityN("SayHello", func(ctx task.ActivityContext) (any, error) {
		var name string
		if err := ctx.GetInput(&name); err != nil {
			return nil, err
		}
		return fmt.Sprintf("Hello, %s!", name), nil
	})
	backendClient := client.NewTaskHubGrpcClient(s.daprd.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, backendClient.StartWorkItemListener(ctx, r))

	resp, err := s.daprd.GRPCClient(t, ctx).
		StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
			WorkflowComponent: "dapr",
			WorkflowName:      "SingleActivity",
			Input:             []byte(`"Dapr"`),
			InstanceId:        "myinstance",
		})
	require.NoError(t, err)

	id := api.InstanceID(resp.GetInstanceId())
	metadata, err := backendClient.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, metadata.IsComplete())
	assert.Equal(t, `"Hello, Dapr!"`, metadata.SerializedOutput)
}
