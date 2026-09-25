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

package health

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

const (
	errInfoType = "type.googleapis.com/google.rpc.ErrorInfo"
)

func init() {
	suite.Register(new(errors))
}

type errors struct {
	daprd *procdaprd.Daprd
}

func (e *errors) Setup(t *testing.T) []framework.Option {
	e.daprd = procdaprd.New(t, procdaprd.WithAppID("my-test-app"))
	return []framework.Option{
		framework.WithProcesses(e.daprd),
	}
}

func (e *errors) Run(t *testing.T, ctx context.Context) {
	e.daprd.WaitUntilRunning(t, ctx)
	httpClient := client.HTTP(t)

	// Covers apierrors.HealthAppIDNotMatch: a caller checks the appid query
	// param and gets an error if this daprd's app-id doesn't match.
	t.Run("app id not match", func(t *testing.T) {
		url := fmt.Sprintf("http://localhost:%d/v1.0/healthz?appid=wrong-app-id", e.daprd.HTTPPort())
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusInternalServerError, resp.StatusCode)

		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var data map[string]any
		require.NoError(t, json.Unmarshal(b, &data))

		assert.Equal(t, "ERR_HEALTH_APPID_NOT_MATCH", data["errorCode"])
		assert.Equal(t, "dapr app-id does not match", data["message"])

		details, ok := data["details"].([]any)
		require.True(t, ok, "details field missing or not an array")
		require.NotEmpty(t, details)

		var foundErrInfo bool
		for _, d := range details {
			dm, ok := d.(map[string]any)
			if !ok {
				continue
			}
			if dm["@type"] == errInfoType {
				assert.Equal(t, "ERR_HEALTH_APPID_NOT_MATCH", dm["reason"])
				foundErrInfo = true
				break
			}
		}
		require.True(t, foundErrInfo, "ErrorInfo detail not found in response")
	})
}
