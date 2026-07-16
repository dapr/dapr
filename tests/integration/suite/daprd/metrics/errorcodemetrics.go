/*
Copyright 2024 The Dapr Authors
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

package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(errorcodemetrics))
}

// errorcodemetrics tests daprd error code metrics for workflows
type errorcodemetrics struct {
	daprd *daprd.Daprd
	place *placement.Placement
}

func (e *errorcodemetrics) Setup(t *testing.T) []framework.Option {
	e.place = placement.New(t)

	app := app.New(t)

	e.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithAppID("myapp"),
		daprd.WithPlacementAddresses(e.place.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithLogLevel("debug"),
		daprd.WithErrorCodeMetrics(t),
	)

	return []framework.Option{
		framework.WithProcesses(e.place, app, e.daprd),
	}
}

func (e *errorcodemetrics) Run(t *testing.T, ctx context.Context) {
	e.place.WaitUntilRunning(t, ctx)
	e.daprd.WaitUntilAppHealth(t, ctx)

	t.Run("gRPC workflow error metrics", func(t *testing.T) {
		// Try to get a non-existent workflow instance which should trigger "ERR_GET_WORKFLOW"
		gclient := e.daprd.GRPCClient(t, ctx)
		for range 2 {
			_, err := gclient.PurgeWorkflowBeta1(ctx, &rtv1.PurgeWorkflowRequest{
				InstanceId:        "non-existent-id",
				WorkflowComponent: "dapr",
			})
			require.Error(t, err)
		}

		// Check for metric count and code
		assert.Eventually(t, func() bool {
			return e.daprd.Metrics(t, ctx).MatchMetricAndSum(t, 2, "dapr_error_code_total", "category:workflow", "error_code:ERR_INSTANCE_ID_NOT_FOUND")
		}, 5*time.Second, 100*time.Millisecond)
	})

	t.Run("HTTP conversation error metrics", func(t *testing.T) {
		// Try to get a non-existent component which should trigger "ERR_DIRECT_INVOKE"
		for range 3 {
			e.daprd.HTTPGet(t, ctx, "/v1.0/workflow/dapr/non-existent-id", 404)
		}

		// Check for metric count and code
		assert.Eventually(t, func() bool {
			return e.daprd.Metrics(t, ctx).MatchMetricAndSum(t, 3, "dapr_error_code_total", "category:service-invocation", "error_code:ERR_DIRECT_INVOKE")
		}, 5*time.Second, 100*time.Millisecond)
	})
}
