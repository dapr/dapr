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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

const (
	ErrInfoType = "type.googleapis.com/google.rpc.ErrorInfo"
)

func init() {
	suite.Register(new(errors))
}

// errors tests the rich error responses for the Distributed Lock HTTP API.
// Only errors that can be triggered without an external lock store backend are
// covered here (store-level errors require a real lock store component).
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

	t.Run("expiry in seconds not positive", func(t *testing.T) {
		url := fmt.Sprintf("http://localhost:%d/v1.0-alpha1/lock/mystore", e.daprd.HTTPPort())
		body := `{"resourceId":"resource","lockOwner":"owner","expiryInSeconds":0}`

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusBadRequest, resp.StatusCode)

		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var data map[string]any
		require.NoError(t, json.Unmarshal(b, &data))

		assert.Equal(t, "ERR_MALFORMED_REQUEST", data["errorCode"])
		assert.Equal(t, "ExpiryInSeconds is not positive in lock store mystore", data["message"])

		details, ok := data["details"].([]any)
		require.True(t, ok)
		require.NotEmpty(t, details)

		var errInfo map[string]any
		for _, d := range details {
			dm, ok := d.(map[string]any)
			if !ok {
				continue
			}
			if dm["@type"] == ErrInfoType {
				errInfo = dm
				break
			}
		}
		require.NotNil(t, errInfo, "ErrorInfo detail not found")
		assert.Equal(t, "ERR_MALFORMED_REQUEST", errInfo["reason"])
	})

	t.Run("lock store not configured", func(t *testing.T) {
		url := fmt.Sprintf("http://localhost:%d/v1.0-alpha1/lock/mystore", e.daprd.HTTPPort())
		body := `{"resourceId":"resource","lockOwner":"owner","expiryInSeconds":10}`

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusInternalServerError, resp.StatusCode)

		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var data map[string]any
		require.NoError(t, json.Unmarshal(b, &data))

		assert.Equal(t, "ERR_LOCK_STORE_NOT_CONFIGURED", data["errorCode"])
		assert.Equal(t, "lock store is not configured", data["message"])

		details, ok := data["details"].([]any)
		require.True(t, ok)
		require.NotEmpty(t, details)

		var errInfo map[string]any
		for _, d := range details {
			dm, ok := d.(map[string]any)
			if !ok {
				continue
			}
			if dm["@type"] == ErrInfoType {
				errInfo = dm
				break
			}
		}
		require.NotNil(t, errInfo, "ErrorInfo detail not found")
		assert.Equal(t, "ERR_LOCK_STORE_NOT_CONFIGURED", errInfo["reason"])
	})

	t.Run("unlock lock store not configured", func(t *testing.T) {
		url := fmt.Sprintf("http://localhost:%d/v1.0-alpha1/unlock/mystore", e.daprd.HTTPPort())
		body := `{"resourceId":"resource","lockOwner":"owner"}`

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusInternalServerError, resp.StatusCode)

		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var data map[string]any
		require.NoError(t, json.Unmarshal(b, &data))

		assert.Equal(t, "ERR_LOCK_STORE_NOT_CONFIGURED", data["errorCode"])
		assert.Equal(t, "lock store is not configured", data["message"])
	})
}
