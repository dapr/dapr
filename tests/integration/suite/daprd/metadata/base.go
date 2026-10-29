/*
Copyright 2023 The Dapr Authors
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

package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(metadata))
}

// metadata tests Dapr's response to metadata API requests.
type metadata struct {
	proc *procdaprd.Daprd
	app  *app.App
}

func (m *metadata) Setup(t *testing.T) []framework.Option {
	m.app = app.New(t)
	subComponentAndConfiguration := `
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: pubsub
spec:
  type: pubsub.in-memory
  version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
  name: sub
spec:
  topic: B
  route: /B
  pubsubname: pubsub
`
	m.proc = procdaprd.New(t,
		procdaprd.WithResourceFiles(subComponentAndConfiguration),
		procdaprd.WithAppPort(m.app.Port()),
	)
	return []framework.Option{
		framework.WithProcesses(m.app, m.proc),
	}
}

func (m *metadata) Run(t *testing.T, parentCtx context.Context) {
	m.proc.WaitUntilRunning(t, parentCtx)

	httpClient := client.HTTP(t)

	t.Run("test HTTP", func(t *testing.T) {
		tests := map[string]string{
			"public endpoint": fmt.Sprintf("http://localhost:%d/v1.0/metadata", m.proc.PublicPort()),
			"API endpoint":    fmt.Sprintf("http://localhost:%d/v1.0/metadata", m.proc.HTTPPort()),
		}
		for testName, reqURL := range tests {
			t.Run(testName, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(parentCtx, time.Second*5)
				defer cancel()

				req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
				require.NoError(t, err)

				resp, err := httpClient.Do(req)
				require.NoError(t, err)
				defer resp.Body.Close()

				validateResponse(t, m.proc.AppID(), m.app.Port(), resp.Body)
			})
		}
	})

	t.Run("public port is read-only", func(t *testing.T) {
		put := func(port int, key string) int {
			reqURL := fmt.Sprintf("http://localhost:%d/v1.0/metadata/%s", port, key)
			req, err := http.NewRequestWithContext(parentCtx, http.MethodPut, reqURL, strings.NewReader("value"))
			require.NoError(t, err)
			resp, err := httpClient.Do(req)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			return resp.StatusCode
		}
		extended := func() map[string]string {
			reqURL := fmt.Sprintf("http://localhost:%d/v1.0/metadata", m.proc.HTTPPort())
			req, err := http.NewRequestWithContext(parentCtx, http.MethodGet, reqURL, nil)
			require.NoError(t, err)
			resp, err := httpClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			var body struct {
				Extended map[string]string `json:"extended"`
			}
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
			return body.Extended
		}

		require.Equal(t, http.StatusNotFound, put(m.proc.PublicPort(), "publickey"))
		require.NotContains(t, extended(), "publickey")

		require.Equal(t, http.StatusNoContent, put(m.proc.HTTPPort(), "apikey"))
		require.Equal(t, "value", extended()["apikey"])
	})
}

// validateResponse asserts that the response body is valid JSON
// and contains the expected fields.
func validateResponse(t *testing.T, appID string, appPort int, body io.Reader) {
	bodyMap := map[string]any{}
	err := json.NewDecoder(body).Decode(&bodyMap)
	require.NoError(t, err)

	require.Equal(t, appID, bodyMap["id"])
	require.Regexp(t, `^(edge|\d+\.\d+\.\d+(-rc\.\d+)?)$`, bodyMap["runtimeVersion"])

	extended, ok := bodyMap["extended"].(map[string]any)
	require.True(t, ok)
	require.Regexp(t, `^(edge|\d+\.\d+\.\d+(-rc\.\d+)?)$`, extended["daprRuntimeVersion"])

	appConnectionProperties, ok := bodyMap["appConnectionProperties"].(map[string]any)
	require.True(t, ok)
	port, ok := appConnectionProperties["port"].(float64)
	require.True(t, ok)
	require.Equal(t, appPort, int(port))
	require.Equal(t, "http", appConnectionProperties["protocol"])
	require.Equal(t, "127.0.0.1", appConnectionProperties["channelAddress"])

	// validate that the metadata contains correct format of subscription.
	// The http response struct is private, so we use assert here.
	subscriptions, ok := bodyMap["subscriptions"].([]any)
	require.True(t, ok)
	subscription, ok := subscriptions[0].(map[string]any)
	require.True(t, ok)
	rules, ok := subscription["rules"].([]any)
	require.True(t, ok)
	rule, ok := rules[0].(map[string]any)
	require.True(t, ok)
	require.Empty(t, rule["match"])
	require.Equal(t, "/B", rule["path"])
}
