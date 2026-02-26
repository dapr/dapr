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
	"time"

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
	e.daprd = procdaprd.New(t, procdaprd.WithAppID("health-errors-app"))

	return []framework.Option{
		framework.WithProcesses(e.daprd),
	}
}

func (e *errors) Run(t *testing.T, ctx context.Context) {
	e.daprd.WaitUntilTCPReady(t, ctx)
	httpClient := client.HTTP(t)

	t.Run("healthz not ready startup error", func(t *testing.T) {
		assertStartupHealthResponse(t, ctx, httpClient, fmt.Sprintf("http://%s/v1.0/healthz", e.daprd.HTTPAddress()),
			"ERR_HEALTH_NOT_READY")
	})

	t.Run("outbound healthz not ready startup error", func(t *testing.T) {
		assertStartupHealthResponse(t, ctx, httpClient, fmt.Sprintf("http://%s/v1.0/healthz/outbound", e.daprd.HTTPAddress()),
			"ERR_OUTBOUND_HEALTH_NOT_READY")
	})

	e.daprd.WaitUntilRunning(t, ctx)

	t.Run("appid mismatch error", func(t *testing.T) {
		data, statusCode, ok := requestErrorBody(t, ctx, httpClient, fmt.Sprintf("http://%s/v1.0/healthz?appid=other-app", e.daprd.HTTPAddress()))
		require.True(t, ok)
		require.Equal(t, http.StatusInternalServerError, statusCode)
		require.Equal(t, "ERR_HEALTH_APPID_NOT_MATCH", data["errorCode"])
		require.True(t, hasErrorInfoDetail(data, "ERR_HEALTH_APPID_NOT_MATCH"))
	})
}

func requestErrorBody(t *testing.T, ctx context.Context, httpClient *http.Client, url string) (map[string]any, int, bool) {
	t.Helper()

	cctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	require.NoError(t, err)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, false
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var data map[string]any
	if jsonErr := json.Unmarshal(body, &data); jsonErr != nil {
		return nil, resp.StatusCode, false
	}

	return data, resp.StatusCode, true
}

func hasErrorInfoDetail(data map[string]any, reason string) bool {
	rawDetails, ok := data["details"].([]any)
	if !ok {
		return false
	}

	for _, rawDetail := range rawDetails {
		detail, ok := rawDetail.(map[string]any)
		if !ok {
			continue
		}

		if detail["@type"] == errInfoType && detail["reason"] == reason {
			return true
		}
	}

	return false
}

func assertStartupHealthResponse(t *testing.T, ctx context.Context, httpClient *http.Client, url, reason string) {
	t.Helper()

	require.Eventually(t, func() bool {
		data, statusCode, ok := requestErrorBody(t, ctx, httpClient, url)
		if !ok {
			return false
		}

		if statusCode == http.StatusNoContent {
			// Some environments complete startup before the first request after TCP readiness.
			return true
		}

		if statusCode != http.StatusInternalServerError {
			return false
		}

		if data["errorCode"] != reason {
			return false
		}

		return hasErrorInfoDetail(data, reason)
	}, 10*time.Second, 10*time.Millisecond)
}
