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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(state))
}

type state struct {
	workflow *workflow.Workflow
}

func (s *state) Setup(t *testing.T) []framework.Option {
	// 1MB payload. Enough memory to be larger than the background variant memory
	// so we can measure (actor) workflow history memory does not leak.
	input := bytes.Repeat([]byte("0"), 1024*1024)

	s.workflow = workflow.New(t,
		workflow.WithAddOrchestrator(t, "foo", func(ctx *task.OrchestrationContext) (any, error) {
			require.NoError(t, ctx.CallActivity("bar", task.WithActivityInput(input)).Await(new([]byte)))
			return "", nil
		}),
		workflow.WithAddActivity(t, "bar", func(ctx task.ActivityContext) (any, error) { return "", nil }),
		workflow.WithScheduler(true),
	)

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *state) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)
	client := s.workflow.BackendClient(t, ctx)
	gclient := s.workflow.GRPCClient(t, ctx)

	var actorMemBaseline float64

	for i := range 5 {
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

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.InDelta(c,
			s.workflow.Metrics(t, ctx)["process_resident_memory_bytes"]*1e-6,
			actorMemBaseline,
			35,
			"workflow memory leak",
		)
	}, time.Second*30, time.Millisecond*10)
}
