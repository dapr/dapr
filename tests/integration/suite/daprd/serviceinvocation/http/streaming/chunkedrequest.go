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
	"strconv"
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
	suite.Register(new(chunkedrequest))
}

// chunkedrequest tests that a chunked-transfer (streaming) request body
// flows through the sidecar without being fully buffered in memory.
type chunkedrequest struct {
	daprdSender   *daprd.Daprd
	daprdReceiver *daprd.Daprd

	sinkCalls atomic.Int32
}

func (c *chunkedrequest) Setup(t *testing.T) []framework.Option {
	receiverApp := app.New(t,
		app.WithHandlerFunc("/healthz", func(http.ResponseWriter, *http.Request) {}),
		app.WithHandlerFunc("/sink", func(w http.ResponseWriter, r *http.Request) {
			c.sinkCalls.Add(1)
			n, _ := io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "%d", n)
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

func (c *chunkedrequest) Run(t *testing.T, ctx context.Context) {
	c.daprdSender.WaitUntilRunning(t, ctx)
	c.daprdReceiver.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	// Send a 1MB chunked request through service invocation.
	// Using io.Pipe ensures the body has no known Content-Length (-1),
	// which triggers chunked transfer encoding.
	// With the fix, the sidecar should NOT buffer this in memory for retries.
	const dataSize = 1 << 20

	pr, pw := io.Pipe()
	go func() {
		chunk := []byte(strings.Repeat("A", 1024))
		for range dataSize / 1024 {
			pw.Write(chunk)
		}
		pw.Close()
	}()

	c.sinkCalls.Store(0)
	url := fmt.Sprintf("http://%s/v1.0/invoke/%s/method/sink",
		c.daprdSender.HTTPAddress(), c.daprdReceiver.AppID())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, pr)
	require.NoError(t, err)

	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	// The sink returns the byte count received
	assert.Equal(t, strconv.Itoa(dataSize), string(respBody))
	// Should only be called once (no retries for streaming requests)
	assert.Equal(t, int32(1), c.sinkCalls.Load())
}
