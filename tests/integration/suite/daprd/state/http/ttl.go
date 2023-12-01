/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(ttl))
}

type ttl struct {
	daprd *procdaprd.Daprd
}

func (l *ttl) Setup(t *testing.T) []framework.Option {
	l.daprd = procdaprd.New(t, procdaprd.WithInMemoryActorStateStore("mystore"))

	return []framework.Option{
		framework.WithProcesses(l.daprd),
	}
}

func (l *ttl) Run(t *testing.T, ctx context.Context) {
	l.daprd.WaitUntilRunning(t, ctx)

	postURL := fmt.Sprintf("http://localhost:%d/v1.0/state/mystore", l.daprd.HTTPPort())

	client := util.HTTPClient(t)

	now := time.Now()

	t.Run("set key with ttl", func(t *testing.T) {
		reqBody := `[{"key": "key1", "value": "value1", "metadata": {"ttlInSeconds": "3"}}]`
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(reqBody))
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	})

	t.Run("ensure key return ttlExpireTime", func(t *testing.T) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, postURL+"/key1", nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, `"value1"`, string(body))

		ttlExpireTimeStr := resp.Header.Get("metadata.ttlExpireTime")
		require.NotEmpty(t, ttlExpireTimeStr)
		ttlExpireTime, err := time.Parse(time.RFC3339, ttlExpireTimeStr)
		require.NoError(t, err)
		assert.InDelta(t, now.Add(3*time.Second).Unix(), ttlExpireTime.Unix(), 1)
	})

	t.Run("ensure key is deleted after ttl", func(t *testing.T) {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, postURL+"/key1", nil)
			require.NoError(c, err)
			resp, err := client.Do(req)
			require.NoError(c, err)
			require.NoError(t, resp.Body.Close())
			assert.Equal(c, http.StatusNoContent, resp.StatusCode)
		}, 5*time.Second, 100*time.Millisecond)
	})
}
