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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(metadata))
}

// metadata tests Dapr's response to metadata API requests.
type metadata struct {
	daprd *daprd.Daprd
}

func (m *metadata) Setup(t *testing.T) []framework.Option {
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
	m.daprd = daprd.New(t, daprd.WithResourceFiles(subComponentAndConfiguration))
	return []framework.Option{
		framework.WithProcesses(m.daprd),
	}
}

func (m *metadata) Run(t *testing.T, ctx context.Context) {
	m.daprd.WaitUntilRunning(t, ctx)

	client := util.HTTPClient(t)

	t.Run("test HTTP", func(t *testing.T) {
		tests := map[string]string{
			"metadata endpoint": fmt.Sprintf("http://localhost:%d/v1.0/metadata", m.daprd.MetadataPort()),
			"API endpoint":      fmt.Sprintf("http://localhost:%d/v1.0/metadata", m.daprd.HTTPPort()),
		}
		for testName, reqURL := range tests {
			t.Run(testName, func(t *testing.T) {
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
				require.NoError(t, err)

				resp, err := client.Do(req)
				require.NoError(t, err)
				defer resp.Body.Close()

				validateResponse(t, m.daprd.AppID(), m.daprd.AppPort(), resp.Body)
			})
		}
	})

	t.Run("metadata endpoint not available on public port", func(t *testing.T) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://localhost:%d/v1.0/metadata", m.daprd.PublicPort()), nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}

// validateResponse asserts that the response body is valid JSON
// and contains the expected fields.
func validateResponse(t *testing.T, appID string, appPort int, body io.Reader) {
	bodyMap := map[string]interface{}{}
	err := json.NewDecoder(body).Decode(&bodyMap)
	require.NoError(t, err)

	require.Equal(t, appID, bodyMap["id"])
	require.Equal(t, "edge", bodyMap["runtimeVersion"])

	extended, ok := bodyMap["extended"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "edge", extended["daprRuntimeVersion"])

	appConnectionProperties, ok := bodyMap["appConnectionProperties"].(map[string]interface{})
	require.True(t, ok)
	port, ok := appConnectionProperties["port"].(float64)
	require.True(t, ok)
	require.Equal(t, appPort, int(port))
	require.Equal(t, "http", appConnectionProperties["protocol"])
	require.Equal(t, "127.0.0.1", appConnectionProperties["channelAddress"])

	// validate that the metadata contains correct format of subscription.
	// The http response struct is private, so we use assert here.
	subscriptions, ok := bodyMap["subscriptions"].([]interface{})
	require.True(t, ok)
	subscription, ok := subscriptions[0].(map[string]interface{})
	require.True(t, ok)
	rules, ok := subscription["rules"].([]interface{})
	require.True(t, ok)
	rule, ok := rules[0].(map[string]interface{})
	require.True(t, ok)
	require.Empty(t, rule["match"])
	require.Equal(t, "/B", rule["path"])
}
