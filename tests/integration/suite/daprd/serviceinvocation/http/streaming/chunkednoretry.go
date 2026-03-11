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
	suite.Register(new(chunkednoretry))
}

// chunkednoretry tests that a chunked-transfer request which receives an error
// response is NOT retried, since the streaming body cannot be replayed.
type chunkednoretry struct {
	daprdSender   *daprd.Daprd
	daprdReceiver *daprd.Daprd

	errorSinkCalls atomic.Int32
}

func (c *chunkednoretry) Setup(t *testing.T) []framework.Option {
	receiverApp := app.New(t,
		app.WithHandlerFunc("/healthz", func(http.ResponseWriter, *http.Request) {}),
		app.WithHandlerFunc("/error-sink", func(w http.ResponseWriter, r *http.Request) {
			c.errorSinkCalls.Add(1)
			io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":"intentional error"}`))
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

func (c *chunkednoretry) Run(t *testing.T, ctx context.Context) {
	c.daprdSender.WaitUntilRunning(t, ctx)
	c.daprdReceiver.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	pr, pw := io.Pipe()
	go func() {
		pw.Write([]byte("streaming data"))
		pw.Close()
	}()

	url := fmt.Sprintf("http://%s/v1.0/invoke/%s/method/error-sink",
		c.daprdSender.HTTPAddress(), c.daprdReceiver.AppID())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, pr)
	require.NoError(t, err)

	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Should get the 500 status from the target app
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	// Should only be called once - no retries for streaming requests
	assert.Equal(t, int32(1), c.errorSinkCalls.Load())
}
