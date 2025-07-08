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

package rerun

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
)

func init() {
	suite.Register(new(nosource))
}

type nosource struct {
	workflow *workflow.Workflow
}

func (n *nosource) Setup(t *testing.T) []framework.Option {
	n.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(n.workflow),
	}
}

func (n *nosource) Run(t *testing.T, ctx context.Context) {
	n.workflow.WaitUntilRunning(t, ctx)

	client := n.workflow.BackendClient(t, ctx, 0)
	_, err := client.RerunWorkflowFromEvent(ctx, api.InstanceID("abc"), 0)

	assert.Equal(t, status.Error(codes.NotFound, "workflow instance does not exist with ID 'abc'"), err)
}
