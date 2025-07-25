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

package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
	_ "github.com/dapr/dapr/tests/integration/suite/daprd/state/http/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	suite.Register(new(httpTest))
	fmt.Println("Registering HTTP state test")
}

type httpTest struct {
	daprd *procdaprd.Daprd
}

func (h *httpTest) Setup(t *testing.T) []framework.Option {
	h.daprd = procdaprd.New(t, procdaprd.WithInMemoryStateStore("mystore"))

	return []framework.Option{
		framework.WithProcesses(h.daprd),
	}
}

func (h *httpTest) Run(t *testing.T, ctx context.Context) {
	h.daprd.WaitUntilRunning(t, ctx)

	postURL := fmt.Sprintf("http://localhost:%d/v1.0/state/mystore", h.daprd.HTTPPort())

	httpClient := client.HTTP(t)

	testData := map[string]string{
		"key1":     "value1",
		"key2/key": "value2",
	}

	for key, value := range testData {
		saveKey(t, httpClient, postURL, key, value, ctx)
	}

	t.Run("Successful retrieval", func(t *testing.T) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, postURL+"/key1", nil)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		assert.Equal(t, `"value1"`, string(body))

	})

	t.Run("Slash containing keys", func(t *testing.T) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, postURL+"?key=key2/key", nil)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, `"value2"`, string(body))
	})

	t.Run("Non-existent key", func(t *testing.T) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, postURL+"/non-existent-key", nil)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, 204, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, 0, len(body), "Always give empty body with 204")
	})

}

func saveKey(t *testing.T, httpClient *http.Client, postURL, key string, value string, ctx context.Context) {
	stateArray := []map[string]string{
		{
			"key":   key,
			"value": value,
		},
	}

	data, err := json.Marshal(stateArray)
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, bytes.NewReader(data))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}
