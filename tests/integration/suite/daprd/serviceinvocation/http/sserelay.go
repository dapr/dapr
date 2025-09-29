/*
copyright 2025 The Dapr Authors
licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
you may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
wITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
see the License for the specific language governing permissions and
limitations under the License.
*/

package http

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(sserelay))
}

type sserelay struct {
	daprdA *daprd.Daprd
	daprdB *daprd.Daprd

	srvA *app.App
	srvB *app.App
}

func (s *sserelay) Setup(t *testing.T) []framework.Option {
	s.srvA = app.New(t,
		app.WithHandlerFunc("/healthz", func(http.ResponseWriter, *http.Request) {}),
		app.WithHandlerFunc("/TOA", func(w http.ResponseWriter, r *http.Request) {
			log.Printf(">>SRVA>>%s\n", r.URL.Path)
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			w.Header().Set("X-Accel-Buffering", "no")

			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
				return
			}

			// Simulate a stream that sends a line every 300–700ms

			for count := range 5 {
				now := time.Now().Format(time.RFC3339Nano)
				payload := fmt.Sprintf("data: {\"n\":%d,\"t\":%q}\n", count, now)
				_, err := w.Write([]byte(payload))
				if err != nil {
					t.Error(err)
					return
				}

				log.Printf(">>SRVA>> sent %d bytes: %q\n", len(payload), payload)
				flusher.Flush()
				select {
				case <-time.After(600 * time.Millisecond):
				case <-r.Context().Done():
					assert.Fail(t, "client closed connection")
					return
				}
			}
		}),
	)

	s.srvB = app.New(t,
		app.WithHandlerFunc("/healthz", func(http.ResponseWriter, *http.Request) {}),
		app.WithHandlerFunc("/TOB", func(w http.ResponseWriter, r *http.Request) {
			url := fmt.Sprintf("http://%s/v1.0/invoke/%s/method/TOA", s.daprdB.HTTPAddress(), s.daprdA.AppID())
			req, err := http.NewRequest(http.MethodGet, url, nil)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			req.Header.Set("Accept", "text/event-stream")
			req.Header.Set("Cache-Control", "no-cache")
			req.Header.Set("Accept-Encoding", "identity")

			resp, err := client.HTTP(t).Do(req)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			defer resp.Body.Close()
			log.Printf(">SRVB>>GOT>> status=%s proto=%s\n", resp.Status, resp.Proto)
			// require.Equal(t, http.StatusOK, resp.StatusCode)

			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache, no-transform")
			w.Header().Set("Connection", "keep-alive")
			w.Header().Set("X-Accel-Buffering", "no")

			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
				return
			}

			log.Printf(">>SRVB>>/relay: piping %s → client (chunking/flush per SSE event)\n", url)

			// Use direct reader for real-time streaming
			reader := bufio.NewReader(resp.Body)
			for {
				line, rErr := reader.ReadString('\n')
				if rErr == io.EOF {
					return
				}
				if rErr != nil {
					http.Error(w, rErr.Error(), http.StatusInternalServerError)
					return
				}

				if len(line) > 0 {
					log.Printf(">>SRVB>> recv %d bytes: %q\n", len(line), line)

					// Forward exactly what we received (includes \n)
					io.WriteString(w, line)

					// Flush immediately for real-time streaming
					flusher.Flush()
				}
			}
		}),
	)

	s.daprdA = daprd.New(t, daprd.WithAppID("daprdA"), daprd.WithAppPort(s.srvA.Port()))
	s.daprdB = daprd.New(t, daprd.WithAppID("daprdB"), daprd.WithAppPort(s.srvB.Port()))

	return []framework.Option{
		framework.WithProcesses(s.srvA, s.srvB, s.daprdA, s.daprdB),
	}
}

func (s *sserelay) Run(t *testing.T, ctx context.Context) {
	s.daprdA.WaitUntilRunning(t, ctx)
	s.daprdB.WaitUntilRunning(t, ctx)

	client := client.HTTP(t)

	url := fmt.Sprintf("http://localhost:%d/TOB", s.srvB.Port())
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Accept-Encoding", "identity")

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	log.Printf("status=%s proto=%s", resp.Status, resp.Proto)

	// Verify SSE headers
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	var total int
	var eventCount int

	// Use direct reader for real-time streaming
	reader := bufio.NewReader(resp.Body)
	for {
		line, rErr := reader.ReadString('\n')
		if rErr == io.EOF {
			return
		}
		require.NoError(t, rErr)

		total += len(line)
		eventCount++
		log.Printf(">>TEST>> recv %d bytes: %q\n", len(line), line)

		// Stop after receiving a certain number of events for testing
		if eventCount >= 5 {
			break
		}
	}

	log.Printf("done; received %d bytes, %d events", total, eventCount)
}
