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
	"sync/atomic"
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
	suite.Register(new(streamerror))
}

// streamerror tests that a non-2xx streaming error response is streamed
// through to the caller without being buffered in memory.
type streamerror struct {
	daprdSender   *daprd.Daprd
	daprdReceiver *daprd.Daprd

	streamErrorCalls atomic.Int32
}

func (c *streamerror) Setup(t *testing.T) []framework.Option {
	const dataSize = 1 << 20

	receiverApp := app.New(t,
		app.WithHandlerFunc("/healthz", func(http.ResponseWriter, *http.Request) {}),
		app.WithHandlerFunc("/stream-error", func(w http.ResponseWriter, r *http.Request) {
			c.streamErrorCalls.Add(1)
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.WriteHeader(http.StatusInternalServerError)
			// Write 1MB error body in chunks
			chunk := strings.Repeat("E", 1024)
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

func (c *streamerror) Run(t *testing.T, ctx context.Context) {
	c.daprdSender.WaitUntilRunning(t, ctx)
	c.daprdReceiver.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	// Non-2xx responses with streaming bodies should be streamed directly to the
	// caller without buffering via RawDataFull(). Send as chunked (streaming)
	// request via pipe, so the error response body is streamed through without
	// being buffered in memory.
	pr, pw := io.Pipe()
	go func() {
		pw.Write([]byte("trigger"))
		pw.Close()
	}()

	c.streamErrorCalls.Store(0)
	url := fmt.Sprintf("http://%s/v1.0/invoke/%s/method/stream-error",
		c.daprdSender.HTTPAddress(), c.daprdReceiver.AppID())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, pr)
	require.NoError(t, err)

	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Should get the 500 status from the target app
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	// Should receive the full 1MB error body streamed through
	assert.Len(t, respBody, 1<<20)
	// Should only be called once (no retries for streaming requests)
	assert.Equal(t, int32(1), c.streamErrorCalls.Load())
}
