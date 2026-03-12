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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	fos "github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(requestmemory))
}

// requestmemory verifies that streaming a large chunked HTTP request body
// through service invocation does not cause the sidecar to buffer the entire
// payload in memory.
type requestmemory struct {
	daprdSender   *daprd.Daprd
	daprdReceiver *daprd.Daprd
}

func (c *requestmemory) Setup(t *testing.T) []framework.Option {
	// Don't run on very constrained CI runners.
	fos.SkipWindows(t)
	fos.SkipMacOS(t)

	receiverApp := app.New(t,
		app.WithHandlerFunc("/healthz", func(http.ResponseWriter, *http.Request) {}),
		app.WithHandlerFunc("/large-request-sink", func(w http.ResponseWriter, r *http.Request) {
			n, _ := io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "%d", n)
		}),
	)

	c.daprdReceiver = daprd.New(t,
		daprd.WithAppPort(receiverApp.Port()),
		daprd.WithMaxBodySize("512Mi"),
	)

	senderApp := app.New(t,
		app.WithHandlerFunc("/healthz", func(http.ResponseWriter, *http.Request) {}),
	)
	c.daprdSender = daprd.New(t,
		daprd.WithAppPort(senderApp.Port()),
		daprd.WithMaxBodySize("512Mi"),
	)

	return []framework.Option{
		framework.WithProcesses(receiverApp, senderApp, c.daprdReceiver, c.daprdSender),
	}
}

func (c *requestmemory) Run(t *testing.T, ctx context.Context) {
	c.daprdSender.WaitUntilRunning(t, ctx)
	c.daprdReceiver.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	const (
		totalSize = 256 << 20
		chunkSize = 1 << 20
		numChunks = totalSize / chunkSize
	)

	// Record baseline memory for both sidecars.
	senderBaseline := c.daprdSender.MetricResidentMemoryMi(t, ctx)
	receiverBaseline := c.daprdReceiver.MetricResidentMemoryMi(t, ctx)

	// Use io.Pipe to produce a request body with unknown Content-Length,
	// forcing chunked transfer encoding.
	pr, pw := io.Pipe()
	go func() {
		chunk := []byte(strings.Repeat("A", chunkSize))
		for range numChunks {
			if _, err := pw.Write(chunk); err != nil {
				pw.CloseWithError(err)
				return
			}
		}
		pw.Close()
	}()

	url := fmt.Sprintf("http://%s/v1.0/invoke/%s/method/large-request-sink",
		c.daprdSender.HTTPAddress(), c.daprdReceiver.AppID())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, pr)
	require.NoError(t, err)

	// Sample memory periodically during the transfer.
	var peakReceiverMi, peakSenderMi atomic.Int64
	storePeak := func(peak *atomic.Int64, val float64) {
		valInt := int64(val * 1000)
		for {
			old := peak.Load()
			if valInt <= old {
				break
			}
			if peak.CompareAndSwap(old, valInt) {
				break
			}
		}
	}

	samplerCtx, samplerCancel := context.WithCancel(ctx)
	defer samplerCancel()
	samplerDone := make(chan struct{})
	go func() {
		defer close(samplerDone)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				storePeak(&peakReceiverMi, c.daprdReceiver.MetricResidentMemoryMi(t, samplerCtx))
				storePeak(&peakSenderMi, c.daprdSender.MetricResidentMemoryMi(t, samplerCtx))
			case <-samplerCtx.Done():
				return
			}
		}
	}()

	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(totalSize), string(body),
		"target app should have received the complete request body")

	// Take a final sample after the transfer.
	storePeak(&peakReceiverMi, c.daprdReceiver.MetricResidentMemoryMi(t, ctx))
	storePeak(&peakSenderMi, c.daprdSender.MetricResidentMemoryMi(t, ctx))

	samplerCancel()
	<-samplerDone

	peakReceiverDelta := float64(peakReceiverMi.Load())/1000 - receiverBaseline
	peakSenderDelta := float64(peakSenderMi.Load())/1000 - senderBaseline

	// If the sidecar buffers the 256MB request body, its RSS would spike by
	// ~256MB. Allow up to 80MB growth for streaming overhead (Go GC, gRPC frame
	// buffers, pipe buffers, runtime allocations).
	const maxDeltaMi = 80

	assert.Less(t, peakReceiverDelta, float64(maxDeltaMi),
		"receiver sidecar should not buffer the streaming request body in memory")
	assert.Less(t, peakSenderDelta, float64(maxDeltaMi),
		"sender sidecar should not buffer the streaming request body in memory")
}
