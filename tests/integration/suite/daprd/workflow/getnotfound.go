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
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	fclient "github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(getnotfound))
}

type getnotfound struct {
	workflow *workflow.Workflow
}

func (g *getnotfound) Setup(t *testing.T) []framework.Option {
	g.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(g.workflow),
	}
}

func (g *getnotfound) Run(t *testing.T, ctx context.Context) {
	g.workflow.WaitUntilRunning(t, ctx)

	t.Run("grpc", func(t *testing.T) {
		gclient := g.workflow.GRPCClient(t, ctx)
		resp, err := gclient.GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{
			InstanceId:        "not-found",
			WorkflowComponent: "dapr",
		})
		require.NoError(t, err)
		assert.Nil(t, resp.GetProperties())
	})

	t.Run("http", func(t *testing.T) {
		req, err := http.NewRequestWithContext(ctx,
			http.MethodGet,
			fmt.Sprintf("http://%s/v1.0-beta1/workflows/dapr/not-found", g.workflow.Dapr().HTTPAddress()),
			nil,
		)
		require.NoError(t, err)

		resp, err := fclient.HTTP(t).Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.JSONEq(t, `{"errorCode":"ERR_INSTANCE_ID_NOT_FOUND","message":"unable to find workflow with the provided instance ID: not-found"}`, string(body))
		require.NoError(t, resp.Body.Close())
	})
}
