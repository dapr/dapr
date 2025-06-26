/*
Copyright 2023 The Dapr Authors
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

package http

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(sse))
}

type sse struct {
	daprd *procdaprd.Daprd
}

func (h *sse) Setup(t *testing.T) []framework.Option {
	newHTTPServer := func() *prochttp.HTTP {
		handler := http.NewServeMux()
		handler.HandleFunc("/events", sseHandler)

		return prochttp.New(t, prochttp.WithHandler(handler))
	}

	srv1 := newHTTPServer()

	h.daprd = procdaprd.New(t, procdaprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: HTTPEndpoint
metadata:
  name: myserver
spec:
  version: v1alpha1
  baseUrl: http://localhost:%d
`, srv1.Port())))

	return []framework.Option{
		framework.WithProcesses(srv1, h.daprd),
	}
}

func (h *sse) Run(t *testing.T, ctx context.Context) {
	h.daprd.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)
	t.Run("invoke sse http endpoint", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://localhost:%d/v1.0/invoke/myserver/method/events", h.daprd.HTTPPort()), nil)
		require.NoError(t, err)

		req.Header.Set("Accept", "text/event-stream")

		resp, err := httpClient.Do(req)
		require.NoError(t, err)

		count := 0

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			event := scanner.Text()

			if strings.HasPrefix(event, "data:") {
				count++
			}
		}

		err = scanner.Err()
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, 10, count)
	})
}

func sseHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	for i := 1; i <= 10; i++ {
		fmt.Fprintf(w, "data: %d\n\n", i)
		flusher.Flush()
		time.Sleep(100 * time.Millisecond)
	}
}
