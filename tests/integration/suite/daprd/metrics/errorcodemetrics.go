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
	"os"
	"path/filepath"
	"testing"

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
	daprd  *daprd.Daprd
	place  *placement.Placement
	resDir string
}

func (e *errorcodemetrics) Setup(t *testing.T) []framework.Option {
	e.place = placement.New(t)

	app := app.New(t)

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: errorcodemetrics
spec:
  metric:
    enabled: true
    recordErrorCodes: true
  metrics:
    enabled: true
    recordErrorCodes: true
`), 0o600))

	e.resDir = t.TempDir()

	e.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithAppID("myapp"),
		daprd.WithPlacementAddresses(e.place.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithLogLevel("debug"),
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(e.resDir),
	)

	return []framework.Option{
		framework.WithProcesses(e.place, app, e.daprd),
	}
}

func (e *errorcodemetrics) Run(t *testing.T, ctx context.Context) {
	e.place.WaitUntilRunning(t, ctx)
	e.daprd.WaitUntilAppHealth(t, ctx)

	t.Run("gRPC workflow error metrics", func(t *testing.T) {
		t.Skip("TODO: @joshvanl: reenable")
		// Try to get a non-existent workflow instance which should trigger "ERR_GET_WORKFLOW"
		gclient := e.daprd.GRPCClient(t, ctx)
		for range 2 {
			_, err := gclient.GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{
				InstanceId:        "non-existent-id",
				WorkflowComponent: "dapr",
			})
			require.Error(t, err)
		}

		// Check for metric count and code
		metrics := e.daprd.Metrics(t, ctx)
		errorMetricName := "dapr_error_code_total|app_id:myapp|category:workflow|error_code:ERR_GET_WORKFLOW"
		assert.Equal(t, 2, int(metrics[errorMetricName]), "Expected \"ERR_GET_WORKFLOW\" to be recorded")
	})

	t.Run("HTTP conversation error metrics", func(t *testing.T) {
		// Try to get a non-existent component which should trigger "ERR_DIRECT_INVOKE"
		for range 3 {
			e.daprd.HTTPGet(t, ctx, "/v1.0/workflow/dapr/non-existent-id", 404)
		}

		// Check for metric count and code
		metrics := e.daprd.Metrics(t, ctx)
		errorMetricName := "dapr_error_code_total|app_id:myapp|category:conversation|error_code:ERR_DIRECT_INVOKE"
		assert.Equal(t, 3, int(metrics[errorMetricName]), "Expected \"ERR_DIRECT_INVOKE\" to be recorded")
	})
}
