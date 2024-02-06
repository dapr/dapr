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
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(httpServerHighCardinality))
}

// httpServerHighCardinality tests daprd metrics for the HTTP server configured with high cardinality
type httpServerHighCardinality struct {
	base
}

func (m *httpServerHighCardinality) Setup(t *testing.T) []framework.Option {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: lowcardinality
spec:
  metrics:
    http:
      increasedCardinality: true
`), 0o600))

	return m.testSetup(t, procdaprd.WithConfigs(configFile))
}

func (m *httpServerHighCardinality) Run(t *testing.T, ctx context.Context) {
	m.beforeRun(t, ctx)

	t.Run("service invocation", func(t *testing.T) {
		reqCtx, reqCancel := context.WithTimeout(ctx, 5*time.Second)
		t.Cleanup(reqCancel)

		// Invoke
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, fmt.Sprintf("http://localhost:%d/v1.0/invoke/myapp/method/hi", m.daprd.HTTPPort()), nil)
		require.NoError(t, err)
		m.doRequest(t, req)

		// Verify metrics
		metrics := m.getMetrics(t, ctx)
		assert.Equal(t, 1, int(metrics["dapr_http_server_request_count|app_id:myapp|method:GET|path:/v1.0/invoke/myapp/method/hi|status:200"]))
	})

	t.Run("state stores", func(t *testing.T) {
		reqCtx, reqCancel := context.WithTimeout(ctx, 5*time.Second)
		t.Cleanup(reqCancel)

		// Write state
		body := `[{"key":"myvalue", "value":"hello world"}]`
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, fmt.Sprintf("http://localhost:%d/v1.0/state/mystore", m.daprd.HTTPPort()), strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("content-type", "application/json")
		m.doRequest(t, req)

		// Get state
		req, err = http.NewRequestWithContext(reqCtx, http.MethodGet, fmt.Sprintf("http://localhost:%d/v1.0/state/mystore/myvalue", m.daprd.HTTPPort()), nil)
		require.NoError(t, err)
		m.doRequest(t, req)

		// Verify metrics
		metrics := m.getMetrics(t, ctx)
		assert.Equal(t, 1, int(metrics["dapr_http_server_request_count|app_id:myapp|method:POST|path:/v1.0/state/mystore|status:204"]))
		assert.Equal(t, 1, int(metrics["dapr_http_server_request_count|app_id:myapp|method:GET|path:/v1.0/state/mystore|status:200"]))
	})
}
