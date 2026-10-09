/*
Copyright 2026 The Dapr Authors
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

package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(errors))
}

type errors struct {
	daprd *procdaprd.Daprd
}

func (e *errors) Setup(t *testing.T) []framework.Option {
	e.daprd = procdaprd.New(t)
	return []framework.Option{
		framework.WithProcesses(e.daprd),
	}
}

func (e *errors) Run(t *testing.T, ctx context.Context) {
	e.daprd.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	// Covers apierrors.Configuration("").StoreNotConfigured()
	t.Run("configuration store not configured", func(t *testing.T) {
		endpoint := fmt.Sprintf("http://localhost:%d/v1.0/configuration/mystore?key=key1", e.daprd.HTTPPort())
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
		require.Equal(t, http.StatusInternalServerError, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		var data map[string]any
		require.NoError(t, json.Unmarshal(body, &data))

		require.Equal(t, "ERR_CONFIGURATION_STORE_NOT_CONFIGURED", data["errorCode"])
		require.Equal(t, "configuration stores not configured", data["message"])

		details, ok := data["details"].([]any)
		require.True(t, ok)
		var errInfo map[string]any
		for _, d := range details {
			m, ok := d.(map[string]any)
			require.True(t, ok)
			if m["@type"] == "type.googleapis.com/google.rpc.ErrorInfo" {
				errInfo = m
			}
		}
		require.NotNil(t, errInfo, "ErrorInfo should be present")
		require.Equal(t, "dapr.io", errInfo["domain"])
		require.Equal(t, "DAPR_CONFIGURATION_STORE_NOT_CONFIGURED", errInfo["reason"])
	})
}
