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
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(idlepurge))
}

type idlepurge struct {
	daprdSender     *daprd.Daprd
	daprdReceiver   *daprd.Daprd
	purged          *logline.LogLine
	idlepurgeEvents int
}

func (c *idlepurge) Setup(t *testing.T) []framework.Option {
	c.idlepurgeEvents = 15

	receiverApp := app.New(t,
		app.WithHandlerFunc("/healthz", func(http.ResponseWriter, *http.Request) {}),
		app.WithHandlerFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
			flusher, ok := w.(http.Flusher)
			if !assert.True(t, ok, "ResponseWriter does not support flushing") {
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("X-Accel-Buffering", "no")
			w.WriteHeader(http.StatusOK)

			for count := range c.idlepurgeEvents {
				_, err := fmt.Fprintf(w, "data: %d\n", count)
				if err != nil {
					return
				}
				flusher.Flush()
				select {
				case <-time.After(500 * time.Millisecond):
				case <-r.Context().Done():
					return
				}
			}
		}),
	)

	c.daprdReceiver = daprd.New(t,
		daprd.WithAppPort(receiverApp.Port()),
	)

	senderApp := app.New(t,
		app.WithHandlerFunc("/healthz", func(http.ResponseWriter, *http.Request) {}),
	)

	c.purged = logline.New(t,
		logline.WithStdoutLineContains("expired gRPC connection(s) to"),
	)

	c.daprdSender = daprd.New(t,
		daprd.WithAppPort(senderApp.Port()),
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_GRPC_MAX_CONN_IDLE", "1s",
			"DAPR_GRPC_CONN_COLLECTOR_INTERVAL", "500ms",
		)),
		daprd.WithLogLineStdout(c.purged),
	)

	return []framework.Option{
		framework.WithProcesses(c.purged, receiverApp, senderApp, c.daprdReceiver, c.daprdSender),
	}
}

func (c *idlepurge) Run(t *testing.T, ctx context.Context) {
	c.daprdSender.WaitUntilRunning(t, ctx)
	c.daprdReceiver.WaitUntilRunning(t, ctx)

	httpClient := client.HTTPWithTimeout(t, 30*time.Second)

	url := fmt.Sprintf("http://%s/v1.0/invoke/%s/method/sse",
		c.daprdSender.HTTPAddress(), c.daprdReceiver.AppID())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var events int
	reader := bufio.NewReader(resp.Body)
	for {
		line, rErr := reader.ReadString('\n')
		if rErr == io.EOF {
			break
		}
		require.NoError(t, rErr, "stream broke after %d of %d events", events, c.idlepurgeEvents)

		if len(line) > 0 {
			assert.Equal(t, fmt.Sprintf("data: %d\n", events), line)
			events++
		}
	}

	assert.Equal(t, c.idlepurgeEvents, events)

	c.purged.EventuallyFoundAll(t)
}
