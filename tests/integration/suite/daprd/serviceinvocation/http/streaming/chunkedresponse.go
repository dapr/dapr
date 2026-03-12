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
	suite.Register(new(chunkedresponse))
}

// chunkedresponse tests that a chunked (streaming) HTTP response from the
// target app is forwarded incrementally through the sidecar without being
// buffered in memory. It verifies this by having the target app send a
// response in two phases: the first chunk is sent immediately, and the
// second chunk only after the test signals via a channel (after reading
// the first chunk). The fact that the first chunk arrives before the
// second is sent proves the response is streamed.
type chunkedresponse struct {
	daprdSender   *daprd.Daprd
	daprdReceiver *daprd.Daprd

	// sendSecondCh is closed by the test after reading the first chunk,
	// telling the app handler to send the second chunk.
	sendSecondCh chan struct{}
}

func (c *chunkedresponse) Setup(t *testing.T) []framework.Option {
	// Use chunks larger than Go's default HTTP server bufio.Writer buffer (4KB)
	// so that writes are flushed to the network immediately rather than waiting
	// for the buffer to fill or the handler to return.
	const chunkSize = 16 * 1024

	c.sendSecondCh = make(chan struct{})

	receiverApp := app.New(t,
		app.WithHandlerFunc("/healthz", func(http.ResponseWriter, *http.Request) {}),
		app.WithHandlerFunc("/chunked-response", func(w http.ResponseWriter, r *http.Request) {
			flusher, ok := w.(http.Flusher)
			if !assert.True(t, ok, "ResponseWriter does not support flushing") {
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.WriteHeader(http.StatusOK)

			// Send first chunk immediately.
			if _, werr := w.Write([]byte(strings.Repeat("A", chunkSize))); werr != nil {
				return
			}
			flusher.Flush()

			// Wait for the test to signal that the first chunk has been
			// received before sending the second chunk. If the sidecar
			// were buffering, the first chunk would never reach the test
			// client and this would block until the context is cancelled.
			select {
			case <-c.sendSecondCh:
			case <-r.Context().Done():
				return
			}

			// Send second chunk.
			if _, werr := w.Write([]byte(strings.Repeat("B", chunkSize))); werr != nil {
				return
			}
			flusher.Flush()
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

func (c *chunkedresponse) Run(t *testing.T, ctx context.Context) {
	c.daprdSender.WaitUntilRunning(t, ctx)
	c.daprdReceiver.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	url := fmt.Sprintf("http://%s/v1.0/invoke/%s/method/chunked-response",
		c.daprdSender.HTTPAddress(), c.daprdReceiver.AppID())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)

	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	const chunkSize = 16 * 1024

	// Read the first chunk. If the sidecar is streaming correctly, this
	// arrives while the app is blocked waiting on sendSecondCh. If the
	// sidecar were buffering the full response, this read would block
	// forever (deadlock) because the app is waiting for us to signal
	// before sending more data.
	firstChunk := make([]byte, chunkSize)
	_, err = io.ReadFull(resp.Body, firstChunk)
	require.NoError(t, err)
	assert.Equal(t, strings.Repeat("A", chunkSize), string(firstChunk))

	// Signal the app to send the second chunk.
	close(c.sendSecondCh)

	// Read the second chunk.
	secondChunk := make([]byte, chunkSize)
	_, err = io.ReadFull(resp.Body, secondChunk)
	require.NoError(t, err)
	assert.Equal(t, strings.Repeat("B", chunkSize), string(secondChunk))

	_, err = resp.Body.Read(make([]byte, 1))
	assert.ErrorIs(t, err, io.EOF)
}
