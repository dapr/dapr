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

package orderingkey

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/workflow/runs"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
)

func init() {
	suite.Register(new(twohosts))
}

// twohosts checks that a key orders the instances of an app across two
// daprds, where the key actor lives on one host and workflow actors on both.
type twohosts struct {
	workflow *workflow.Workflow
}

func (x *twohosts) Setup(t *testing.T) []framework.Option {
	appID := uuid.NewString()
	x.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, daprd.WithAppID(appID)),
		workflow.WithDaprdOptions(1, daprd.WithAppID(appID)),
	)
	return []framework.Option{
		framework.WithProcesses(x.workflow),
	}
}

func (x *twohosts) Run(t *testing.T, ctx context.Context) {
	x.workflow.WaitUntilRunning(t, ctx)
	var r runs.Recorder
	x.workflow.RegistryN(0).AddWorkflowN("wait", r.Waiter)
	x.workflow.RegistryN(1).AddWorkflowN("wait", r.Waiter)
	clients := []*client.TaskHubGrpcClient{x.workflow.BackendClientN(t, ctx, 0), x.workflow.BackendClientN(t, ctx, 1)}

	key := api.WithOrdering(api.Ordering{Key: "shared"})
	ids := []string{"a", "b", "c", "d", "e", "f"}
	for i, id := range ids {
		runs.Start(t, ctx, clients[i%2], id, key)
	}
	for i, id := range ids {
		r.AssertStarted(t, id)
		if i+1 < len(ids) {
			r.AssertQueued(t, ids[i+1:]...)
		}
		r.Finish(t, ctx, clients[(i+1)%2], id)
	}
	require.Equal(t, ids, r.Started())
}
