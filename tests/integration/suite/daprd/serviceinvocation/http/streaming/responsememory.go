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
	suite.Register(new(responsememory))
}

// responsememory verifies that streaming a large HTTP response through service
// invocation does not cause the sidecar to buffer the entire payload in memory.
type responsememory struct {
	daprdSender   *daprd.Daprd
	daprdReceiver *daprd.Daprd
}

func (c *responsememory) Setup(t *testing.T) []framework.Option {
	fos.SkipWindows(t)
	fos.SkipMacOS(t)

	const (
		totalSize = 256 << 20
		chunkSize = 1 << 20
		numChunks = totalSize / chunkSize
	)

	chunk := strings.Repeat("X", chunkSize)

	receiverApp := app.New(t,
		app.WithHandlerFunc("/healthz", func(http.ResponseWriter, *http.Request) {}),
		app.WithHandlerFunc("/large-response", func(w http.ResponseWriter, r *http.Request) {
			flusher, ok := w.(http.Flusher)
			if !assert.True(t, ok, "ResponseWriter does not support flushing") {
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.WriteHeader(http.StatusOK)

			for range numChunks {
				_, werr := w.Write([]byte(chunk))
				if werr != nil {
					return
				}
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

func (c *responsememory) Run(t *testing.T, ctx context.Context) {
	c.daprdSender.WaitUntilRunning(t, ctx)
	c.daprdReceiver.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	const totalSize = 256 << 20

	// Record baseline memory for both sidecars before the streaming call.
	senderBaseline := c.daprdSender.MetricResidentMemoryMi(t, ctx)
	receiverBaseline := c.daprdReceiver.MetricResidentMemoryMi(t, ctx)

	url := fmt.Sprintf("http://%s/v1.0/invoke/%s/method/large-response",
		c.daprdSender.HTTPAddress(), c.daprdReceiver.AppID())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)

	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var peakReceiverMi, peakSenderMi atomic.Int64
	storePeak := func(peak *atomic.Int64, val float64) {
		valInt := int64(val * 1000) // Store as milli-Mi for precision.
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
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				storePeak(&peakReceiverMi, c.daprdReceiver.MetricResidentMemoryMi(t, ctx))
				storePeak(&peakSenderMi, c.daprdSender.MetricResidentMemoryMi(t, ctx))
			case <-samplerCtx.Done():
				return
			}
		}
	}()

	n, err := io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	assert.Equal(t, int64(totalSize), n, "should receive the complete response body")

	// Take a final sample after the transfer.
	storePeak(&peakReceiverMi, c.daprdReceiver.MetricResidentMemoryMi(t, ctx))
	storePeak(&peakSenderMi, c.daprdSender.MetricResidentMemoryMi(t, ctx))

	peakReceiverDelta := float64(peakReceiverMi.Load())/1000 - receiverBaseline
	peakSenderDelta := float64(peakSenderMi.Load())/1000 - senderBaseline

	// If the sidecar buffers the 256MB response, its RSS would spike by ~256MB.
	// Allow up to 80MB growth for streaming overhead (Go GC, gRPC frame buffers,
	// pipe buffers, runtime allocations). This is generous enough to avoid
	// flakes but catches a full-body buffer.
	const maxDeltaMi = 80

	assert.Less(t, peakReceiverDelta, float64(maxDeltaMi),
		"receiver sidecar should not buffer the streaming response in memory")
	assert.Less(t, peakSenderDelta, float64(maxDeltaMi),
		"sender sidecar should not buffer the streaming response in memory")

	// Stop the sampler goroutine.
	samplerCancel()
	<-samplerDone
}
