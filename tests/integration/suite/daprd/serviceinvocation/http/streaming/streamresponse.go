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

package streaming

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
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(streamresponse))
}

// streamresponse tests that a chunked (streaming) response flows through
// the sidecar without being fully buffered in memory.
type streamresponse struct {
	daprdSender   *daprd.Daprd
	daprdReceiver *daprd.Daprd
}

func (c *streamresponse) Setup(t *testing.T) []framework.Option {
	const dataSize = 1 << 20

	receiverApp := app.New(t,
		app.WithHandlerFunc("/healthz", func(http.ResponseWriter, *http.Request) {}),
		app.WithHandlerFunc("/stream-response", func(w http.ResponseWriter, r *http.Request) {
			flusher, ok := w.(http.Flusher)
			if !assert.True(t, ok, "ResponseWriter does not support flushing") {
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.WriteHeader(http.StatusOK)
			// Write 1MB in 1KB chunks
			chunk := strings.Repeat("X", 1024)
			for range dataSize / 1024 {
				w.Write([]byte(chunk))
				flusher.Flush()
			}
		}),
	)

	c.daprdReceiver = daprd.New(t,
		daprd.WithAppPort(receiverApp.Port()),
	)

	senderApp := app.New(t,
		app.WithHandlerFunc("/healthz", func(http.ResponseWriter, *http.Request) {}),
	)
	c.daprdSender = daprd.New(t,
		daprd.WithAppPort(senderApp.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(receiverApp, senderApp, c.daprdReceiver, c.daprdSender),
	}
}

func (c *streamresponse) Run(t *testing.T, ctx context.Context) {
	c.daprdSender.WaitUntilRunning(t, ctx)
	c.daprdReceiver.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	// Streamed responses should flow through without buffering
	url := fmt.Sprintf("http://%s/v1.0/invoke/%s/method/stream-response",
		c.daprdSender.HTTPAddress(), c.daprdReceiver.AppID())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)

	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Len(t, respBody, 1<<20)
}
