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
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(Metadata))
}

// Metadata tests Dapr's response to metadata API requests.
type Metadata struct{}

func (*Metadata) Setup(*testing.T, context.Context) []framework.RunDaprdOption {
	return nil
}

func (*Metadata) Run(t *testing.T, ctx context.Context, cmd *framework.Command) {
	assert.Eventually(t, func() bool {
		_, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", cmd.InternalGRPCPort))
		return err == nil
	}, time.Second*5, 100*time.Millisecond)

	reqURL := fmt.Sprintf("http://localhost:%d/v1.0/metadata", cmd.PublicPort)

	ctx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	resBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	validateResponse(t, cmd.AppID, cmd.AppPort, string(resBody))
}

// validateResponse asserts that the response body is valid JSON
// and contains the expected fields.
func validateResponse(t *testing.T, appID string, appPort int, body string) {
	bodyMap := map[string]interface{}{}
	err := json.Unmarshal([]byte(body), &bodyMap)
	require.NoError(t, err)

	require.Equal(t, appID, bodyMap["id"])
	require.Equal(t, "edge", bodyMap["runtimeVersion"])

	extended, ok := bodyMap["extended"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "edge", extended["daprRuntimeVersion"])

	appConnectionProperties, ok := bodyMap["appConnectionProperties"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, appPort, int(appConnectionProperties["port"].(float64)))
	require.Equal(t, "http", appConnectionProperties["protocol"])
	require.Equal(t, "127.0.0.1", appConnectionProperties["channelAddress"])
}
