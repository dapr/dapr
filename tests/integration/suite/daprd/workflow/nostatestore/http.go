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

package nostatestore

import (
	"context"
	"fmt"
	"io"
	nethttp "net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	frameworkclient "github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(http))
}

type http struct {
	daprd *daprd.Daprd
}

func (h *http) Setup(t *testing.T) []framework.Option {
	sched := scheduler.New(t)
	place := placement.New(t)

	h.daprd = daprd.New(t,
		daprd.WithScheduler(sched),
		daprd.WithPlacementAddresses(place.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(sched, place, h.daprd),
	}
}

func (h *http) Run(t *testing.T, ctx context.Context) {
	h.daprd.WaitUntilRunning(t, ctx)

	reg := task.NewTaskRegistry()
	reg.AddOrchestratorN("foo", func(ctx *task.OrchestrationContext) (any, error) {
		return nil, nil
	})

	cl := client.NewTaskHubGrpcClient(h.daprd.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, cl.StartWorkItemListener(ctx, reg))

	reqURL := fmt.Sprintf("http://localhost:%d/v1.0-beta1/workflows/dapr/foo/start", h.daprd.HTTPPort())
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, reqURL, nil)
	require.NoError(t, err)

	resp, err := frameworkclient.HTTP(t).Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, nethttp.StatusInternalServerError, resp.StatusCode)
	assert.Contains(t, string(body), "the state store is not configured to use the actor runtime")
}
