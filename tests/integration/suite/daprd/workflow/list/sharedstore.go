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

package list

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(sharedstore))
}

// sharedstore runs two apps that share one state store. The "_" in the app ID
// "a_b" matches the "x" in "axb" in the LIKE pattern of the store. Each app
// lists only its own instances.
type sharedstore struct {
	workflow *workflow.Workflow
}

func (s *sharedstore) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, daprd.WithAppID("a_b")),
		workflow.WithDaprdOptions(1, daprd.WithAppID("axb")),
	)

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *sharedstore) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	clients := make([]*client.TaskHubGrpcClient, 2)
	for i := range clients {
		s.workflow.RegistryN(i).AddWorkflowN("done", func(ctx *task.WorkflowContext) (any, error) {
			return nil, nil
		})
		clients[i] = s.workflow.BackendClientN(t, ctx, i)
	}

	want := [][]string{{"a-1", "a-2", "a-3"}, {"x-1", "x-2", "x-3"}}
	for i, c := range clients {
		for _, id := range want[i] {
			_, err := c.ScheduleNewWorkflow(ctx, "done", api.WithInstanceID(api.InstanceID(id)))
			require.NoError(t, err)
			_, err = c.WaitForWorkflowCompletion(ctx, api.InstanceID(id))
			require.NoError(t, err)
		}
	}

	for i, c := range clients {
		resp, err := c.ListInstanceIDs(ctx)
		require.NoError(t, err)
		assert.ElementsMatch(t, want[i], resp.GetInstanceIds(), s.workflow.DaprN(i).AppID())

		// Pages of one key. For "a_b", a key of the other app gives an
		// empty page with a token.
		var ids []string
		opts := []api.ListInstanceIDsOptions{api.WithListInstanceIDsPageSize(1)}
		for range 20 {
			resp, err = c.ListInstanceIDs(ctx, opts...)
			require.NoError(t, err)
			ids = append(ids, resp.GetInstanceIds()...)
			if resp.ContinuationToken == nil {
				break
			}
			opts = []api.ListInstanceIDsOptions{
				api.WithListInstanceIDsPageSize(1),
				api.WithListInstanceIDsContinuationToken(resp.GetContinuationToken()),
			}
		}
		assert.Nil(t, resp.ContinuationToken)
		assert.ElementsMatch(t, want[i], ids, s.workflow.DaprN(i).AppID())
	}
}
