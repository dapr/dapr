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
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(metadata))
}

// metadata tests Dapr's response to metadata API requests.
type metadata struct {
	proc *procdaprd.Daprd
}

func (m *metadata) Setup(t *testing.T) []framework.Option {
	m.proc = procdaprd.New(t)
	return []framework.Option{
		framework.WithProcesses(m.proc),
	}
}

func (m *metadata) Run(t *testing.T, parentCtx context.Context) {
	m.proc.WaitUntilRunning(t, parentCtx)

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

				resp, err := http.DefaultClient.Do(req)
				require.NoError(t, err)
				defer resp.Body.Close()

				validateResponse(t, m.proc.AppID(), m.proc.AppPort(), resp.Body)
			})
		}
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
}
