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
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/messages/errorcodes"
	"github.com/dapr/dapr/tests/integration/framework"
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
	// Setup daprd with a component that will fail initialization
	// to trigger health check errors
	r.daprd = procdaprd.New(t,
		procdaprd.WithResourceFiles(`
apiVersion: daprd.io/v1alpha1
kind: Component
metadata:
  name: failing-component
spec:
  type: state.redis
  version: v1
  metadata:
  - name: redisHost
    value: "invalid-host:6379"
  - name: redisPassword
    value: ""
`))

	return []framework.Option{
		framework.WithProcesses(r.daprd),
	}
}

var respError struct {
	Code    string `json:"errorCode"`
	Message string `json:"message"`
}

func (r *richErrors) Run(t *testing.T, ctx context.Context) {
	r.daprd.WaitUntilRunning(t, ctx)

	httpClient := &http.Client{
		Timeout: 5 * time.Second,
	}

	t.Run("health endpoint returns rich error on component failure", func(t *testing.T) {
		// Give some time for component initialization to fail
		assert.Eventually(t, func() bool {
			url := fmt.Sprintf("http://localhost:%d/v1.0/healthz", r.daprd.HTTPPort())
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			require.NoError(t, err)

			resp, err := httpClient.Do(req)
			if err != nil {
				return false
			}
			defer resp.Body.Close()

			// Expect non-200 status when component fails
			return resp.StatusCode != http.StatusOK
		}, 10*time.Second, 100*time.Millisecond, "Expected health check to fail")

		// Now verify the error structure
		url := fmt.Sprintf("http://localhost:%d/v1.0/healthz", r.daprd.HTTPPort())
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		defer resp.Body.Close()

		err = json.Unmarshal(body, &respError)
		require.NoError(t, err)

		assert.Equal(t, errorcodes.HealthNotReady.Code, respError.Code)
		assert.NotEmpty(t, respError.Message)
		assert.Contains(t, respError.Message, "not ready")
	})

	t.Run("outbound health endpoint returns rich errors", func(t *testing.T) {
		url := fmt.Sprintf("http://localhost:%d/v1.0/healthz/outbound", r.daprd.HTTPPort())
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		// Outbound health checks component initialization
		if resp.StatusCode != http.StatusOK {
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			defer resp.Body.Close()

			err = json.Unmarshal(body, &respError)
			require.NoError(t, err)

			// Verify rich error structure
			assert.Equal(t, errorcodes.HealthOutboundNotReady.Code, respError.Code)
			assert.NotEmpty(t, respError.Message)
			assert.Contains(t, respError.Message, "not ready")
		}
	})
}
