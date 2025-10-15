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
package healthz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/messages/errorcodes"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(richErrors))
}

type richErrors struct {
	daprd *procdaprd.Daprd
}

func (r *richErrors) Setup(t *testing.T) []framework.Option {
	// Setup daprd with an app-id that points to a non-existent app
	// This will cause the app channel to fail health checks
	r.daprd = procdaprd.New(t,
		procdaprd.WithAppPort(9999), // Port with no app listening
		procdaprd.WithAppHealthCheck(true),
		procdaprd.WithAppHealthCheckPath("/healthz"),
		procdaprd.WithAppHealthProbeInterval(1),
		procdaprd.WithAppHealthProbeThreshold(1),
	)
	return []framework.Option{
		framework.WithProcesses(r.daprd),
	}
}

func (r *richErrors) Run(t *testing.T, ctx context.Context) {
	httpClient := client.HTTP(t)

	t.Run("health endpoint returns rich error when app not healthy", func(t *testing.T) {
		r.daprd.WaitUntilTCPReady(t, ctx)

		url := fmt.Sprintf("http://localhost:%d/v1.0/healthz", r.daprd.HTTPPort())
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		require.NoError(t, err)

		assert.EventuallyWithT(t, func(t *assert.CollectT) {
			resp, err := httpClient.Do(req)
			require.NoError(t, err)

			// Health check should fail
			assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

			var res respError
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&res))

			// Verify rich error structure
			assert.Equal(t, errorcodes.HealthNotReady.Code, res.Code)
			assert.NotEmpty(t, res.Message)
			assert.Contains(t, res.Message, "not ready")

		}, time.Second*10, 100*time.Millisecond)
	})

	t.Run("outbound health endpoint status check", func(t *testing.T) {
		url := fmt.Sprintf("http://localhost:%d/v1.0/healthz/outbound", r.daprd.HTTPPort())

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		// Outbound health may be 204 (healthy) if no components are configured
		// or may be 500 (unhealthy) if components fail to initialize
		// Test both scenarios
		if resp.StatusCode == http.StatusInternalServerError {
			// If unhealthy, verify rich error structure
			var res respError
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&res))

			assert.Equal(t, errorcodes.HealthOutboundNotReady.Code, res.Code)
			assert.NotEmpty(t, res.Message)
			assert.Contains(t, res.Message, "not ready")
		} else {
			// If healthy, that's also valid (no components configured)
			assert.Equal(t, http.StatusNoContent, resp.StatusCode)
		}
	})
}

type respError struct {
	Code    string `json:"errorCode"`
	Message string `json:"message"`
}
