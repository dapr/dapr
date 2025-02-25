/*
Copyright 2024 The Dapr Authors
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

package state

import (
	"context"
	"fmt"
	"io"
	nethttp "net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(http))
}

type http struct {
	app *actors.Actors
}

func (h *http) Setup(t *testing.T) []framework.Option {
	h.app = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", func(nethttp.ResponseWriter, *nethttp.Request) {
		}),
	)

	return []framework.Option{
		framework.WithProcesses(h.app),
	}
}

func (h *http) Run(t *testing.T, ctx context.Context) {
	h.app.WaitUntilRunning(t, ctx)

	client := client.HTTP(t)

	url := fmt.Sprintf("http://%s/v1.0/actors/abc/123/state/key1", h.app.Daprd().HTTPAddress())
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, nethttp.StatusNoContent, resp.StatusCode)
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, ``, string(b))

	reqBody := `[{"operation":"upsert","request":{"key":"key1","value":"value1"}}]`
	url = fmt.Sprintf("http://%s/v1.0/actors/abc/123/state", h.app.Daprd().HTTPAddress())
	req, err = nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, strings.NewReader(reqBody))
	require.NoError(t, err)
	resp, err = client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, nethttp.StatusBadRequest, resp.StatusCode)
	b, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, `{"errorCode":"ERR_ACTOR_INSTANCE_MISSING","message":"actor instance is missing"}`, string(b))
	assert.Eventually(t, func() bool {
		return h.app.Daprd().Metrics(t, ctx).MatchMetricAndSum(t, 1, "dapr_error_code_total", "category:actor", "error_code:ERR_ACTOR_INSTANCE_MISSING")
	}, 5*time.Second, 100*time.Millisecond)

	url = fmt.Sprintf("http://%s/v1.0/actors/abc/123/method/foo", h.app.Daprd().HTTPAddress())
	req, err = nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
	require.NoError(t, err)
	resp, err = client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, nethttp.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	url = fmt.Sprintf("http://%s/v1.0/actors/abc/123/state/key1", h.app.Daprd().HTTPAddress())
	req, err = nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, url, nil)
	require.NoError(t, err)
	assert.Equal(t, nethttp.StatusOK, resp.StatusCode)
	resp, err = client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, nethttp.StatusNoContent, resp.StatusCode)
	b, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Empty(t, b)
	require.NoError(t, resp.Body.Close())

	reqBody = `[{"operation":"upsert","request":{"key":"key1","value":"value1"}},{"operation":"upsert","request":{"key":"key2","value":"value2"}}]`
	url = fmt.Sprintf("http://%s/v1.0/actors/abc/123/state", h.app.Daprd().HTTPAddress())
	req, err = nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, strings.NewReader(reqBody))
	require.NoError(t, err)
	resp, err = client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, nethttp.StatusNoContent, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	url = fmt.Sprintf("http://%s/v1.0/actors/abc/123/state/key1", h.app.Daprd().HTTPAddress())
	req, err = nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err = client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, nethttp.StatusOK, resp.StatusCode)
	b, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	assert.Equal(t, []byte(`"value1"`), b)
	url = fmt.Sprintf("http://%s/v1.0/actors/abc/123/state/key2", h.app.Daprd().HTTPAddress())
	req, err = nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, url, nil)
	require.NoError(t, err)
	assert.Equal(t, nethttp.StatusOK, resp.StatusCode)
	resp, err = client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, nethttp.StatusOK, resp.StatusCode)
	b, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, []byte(`"value2"`), b)
	require.NoError(t, resp.Body.Close())

	reqBody = `[{"operation":"delete","request":{"key":"key1"}},{"operation":"delete","request":{"key":"key2"}}]`
	url = fmt.Sprintf("http://%s/v1.0/actors/abc/123/state", h.app.Daprd().HTTPAddress())
	req, err = nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, strings.NewReader(reqBody))
	require.NoError(t, err)
	resp, err = client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, nethttp.StatusNoContent, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	url = fmt.Sprintf("http://%s/v1.0/actors/abc/123/state/key1", h.app.Daprd().HTTPAddress())
	req, err = nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err = client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, nethttp.StatusNoContent, resp.StatusCode)
	b, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Empty(t, b)
	require.NoError(t, resp.Body.Close())
	url = fmt.Sprintf("http://%s/v1.0/actors/abc/123/state/key2", h.app.Daprd().HTTPAddress())
	req, err = nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err = client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, nethttp.StatusNoContent, resp.StatusCode)
	b, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Empty(t, b)
	require.NoError(t, resp.Body.Close())
}
