/*
Copyright 2025 The Dapr Authors
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

package tracing

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(otelenv))
}

type otelenv struct {
	daprdNoEnv   *daprd.Daprd
	daprdWithEnv *daprd.Daprd
}

func (o *otelenv) Setup(t *testing.T) []framework.Option {
	// Test default behavior
	o.daprdNoEnv = daprd.New(t,
		daprd.WithAppID("test-app"),
	)

	// Test with OTEL env vars
	o.daprdWithEnv = daprd.New(t,
		daprd.WithAppID("test-app-env"),
		daprd.WithExecOptions(
			exec.WithEnvVars(t,
				"OTEL_SERVICE_NAME", "my-custom-service",
				"OTEL_RESOURCE_ATTRIBUTES", "service.namespace=production,k8s.pod.name=pod-123,k8s.deployment.name=my-app",
			),
		),
	)

	return []framework.Option{
		framework.WithProcesses(o.daprdNoEnv, o.daprdWithEnv),
	}
}

func (o *otelenv) Run(t *testing.T, ctx context.Context) {
	o.daprdNoEnv.WaitUntilRunning(t, ctx)
	o.daprdWithEnv.WaitUntilRunning(t, ctx)

	t.Run("without OTEL env vars", func(t *testing.T) {
		client := o.daprdNoEnv.GRPCClient(t, ctx)
		resp, err := client.GetMetadata(ctx, &runtime.GetMetadataRequest{})
		require.NoError(t, err)
		assert.Equal(t, "test-app", resp.GetId())
		assert.NotEmpty(t, resp.GetRuntimeVersion())
	})

	t.Run("with OTEL env vars", func(t *testing.T) {
		client := o.daprdWithEnv.GRPCClient(t, ctx)
		resp, err := client.GetMetadata(ctx, &runtime.GetMetadataRequest{})
		require.NoError(t, err)
		assert.Equal(t, "test-app-env", resp.GetId())
		assert.NotEmpty(t, resp.GetRuntimeVersion())
	})
}
