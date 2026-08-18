/*
Copyright 2025 The Dapr Authors
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
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(basic))
}

type basic struct {
	daprd *daprd.Daprd
}

const componentYAML = `
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: binarystore.fake
  version: v1
`

func (b *basic) Setup(t *testing.T) []framework.Option {
	b.daprd = daprd.New(t, daprd.WithResourceFiles(componentYAML))
	return []framework.Option{
		framework.WithProcesses(b.daprd),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	b.daprd.WaitUntilRunning(t, ctx)
	base := fmt.Sprintf("http://%s/v1.0-alpha1/binarystore/mystore", b.daprd.HTTPAddress())
	httpClient := client.HTTP(t)

	t.Run("PUT then GET round-trips bytes", func(t *testing.T) {
		payload := []byte("the quick brown fox")

		req, err := http.NewRequestWithContext(ctx, http.MethodPut, base+"/fox.bin", strings.NewReader(string(payload)))
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)

		req, err = http.NewRequestWithContext(ctx, http.MethodGet, base+"/fox.bin", nil)
		require.NoError(t, err)
		resp, err = httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "application/octet-stream", resp.Header.Get("content-type"))
		got, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, payload, got)
	})

	t.Run("POST without overwrite conflicts on existing file", func(t *testing.T) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, base+"/conflict.bin", strings.NewReader("first"))
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusNoContent, resp.StatusCode)

		req, err = http.NewRequestWithContext(ctx, http.MethodPost, base+"/conflict.bin", strings.NewReader("second"))
		require.NoError(t, err)
		resp, err = httpClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, http.StatusConflict, resp.StatusCode)
	})

	t.Run("GET missing file returns 404", func(t *testing.T) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/missing.bin", nil)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("DELETE removes file then GET 404", func(t *testing.T) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, base+"/del.bin", strings.NewReader("bye"))
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		req, err = http.NewRequestWithContext(ctx, http.MethodDelete, base+"/del.bin", nil)
		require.NoError(t, err)
		resp, err = httpClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)

		req, err = http.NewRequestWithContext(ctx, http.MethodGet, base+"/del.bin", nil)
		require.NoError(t, err)
		resp, err = httpClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("component not found returns 400", func(t *testing.T) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://%s/v1.0-alpha1/binarystore/nope/x.bin", b.daprd.HTTPAddress()), nil)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}
