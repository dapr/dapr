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

package memory

import (
	"bytes"
	"context"
	"testing"

	"github.com/microsoft/durabletask-go/api"
	"github.com/microsoft/durabletask-go/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(scheduler))
}

type scheduler struct {
	workflow *workflow.Workflow
}

func (s *scheduler) Setup(t *testing.T) []framework.Option {
	// 2MB payload. Enough memory to be larger than the background variant memory
	// so we can measure (actor) workflow history memory does not leak.
	input := bytes.Repeat([]byte("0"), 2*1024*1024)

	s.workflow = workflow.New(t,
		workflow.WithScheduler(true),
		workflow.WithAddOrchestratorN(t, "foo", func(ctx *task.OrchestrationContext) (any, error) {
			require.NoError(t, ctx.CallActivity("bar", task.WithActivityInput(input)).Await(new([]byte)))
			return "", nil
		}),
		workflow.WithAddActivityN(t, "bar", func(ctx task.ActivityContext) (any, error) { return "", nil }),
	)

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *scheduler) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)
	client := s.workflow.BackendClient(t, ctx)
	gclient := s.workflow.GRPCClient(t, ctx)

	var actorMemBaseline float64

	for i := 0; i < 10; i++ {
		resp, err := gclient.StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
			WorkflowComponent: "dapr",
			WorkflowName:      "foo",
		})
		require.NoError(t, err)
		_, err = client.WaitForOrchestrationCompletion(ctx, api.InstanceID(resp.GetInstanceId()))
		require.NoError(t, err)

		if i == 0 {
			actorMemBaseline = s.workflow.Metrics(t, ctx)["process_resident_memory_bytes"] * 1e-6
		}
	}

	assert.InDelta(t,
		s.workflow.Metrics(t, ctx)["process_resident_memory_bytes"]*1e-6,
		actorMemBaseline,
		35,
		"workflow memory leak",
	)
}
