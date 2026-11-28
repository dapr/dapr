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
	"bytes"
	"context"
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

func init() {
	suite.Register(new(defaultmaxbodysize))
}

// defaultmaxbodysize starts daprd without --max-body-size and checks that the
// HTTP API server accepts a 10 MiB request body and refuses a 17 MiB one.
type defaultmaxbodysize struct {
	daprd *procdaprd.Daprd
}

func (d *defaultmaxbodysize) Setup(t *testing.T) []framework.Option {
	d.daprd = procdaprd.New(t, procdaprd.WithInMemoryStateStore("mystore"))

	return []framework.Option{
		framework.WithProcesses(d.daprd),
	}
}

func (d *defaultmaxbodysize) Run(t *testing.T, ctx context.Context) {
	d.daprd.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)
	stateURL := fmt.Sprintf("http://localhost:%d/v1.0/state/mystore", d.daprd.HTTPPort())

	save := func(t *testing.T, key string, size int) (int, string) {
		t.Helper()

		var body bytes.Buffer
		body.WriteString(`[{"key":"` + key + `","value":"`)
		body.Write(bytes.Repeat([]byte("x"), size))
		body.WriteString(`"}]`)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, stateURL, &body)
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, string(respBody)
	}

	code, body := save(t, "large", 10<<20)
	require.Equal(t, http.StatusNoContent, code, body)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, stateURL+"/large", nil)
	require.NoError(t, err)
	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	got, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode)
	// The value is stored as a JSON string, so the response has two quotes.
	assert.Len(t, got, 10<<20+2)

	code, body = save(t, "too-large", 17<<20)
	assert.Equal(t, http.StatusBadRequest, code, body)
}
