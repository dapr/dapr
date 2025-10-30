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
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
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
	daprdC *daprd.Daprd

	srvA *app.App
	srvB *app.App
	srvC *app.App

	eventsA sync.Map
	eventsB sync.Map
	eventsC sync.Map
}

func (s *sserelay) Setup(t *testing.T) []framework.Option {
	s.srvA = app.New(t,
		app.WithHandlerFunc("/healthz", func(http.ResponseWriter, *http.Request) {}),
		app.WithHandlerFunc("/TOA", func(w http.ResponseWriter, r *http.Request) {
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
				s.eventsA.Store(count, payload)
				_, err := w.Write([]byte(payload))
				if err != nil {
					t.Error(err)
					return
				}

				flusher.Flush()
				select {
				case <-time.After(600 * time.Millisecond):
				case <-r.Context().Done():
					t.Error(r.Context().Err())
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

			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache, no-transform")
			w.Header().Set("Connection", "keep-alive")
			w.Header().Set("X-Accel-Buffering", "no")

			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
				return
			}

			// Use direct reader for real-time streaming
			reader := bufio.NewReader(resp.Body)
			count := 0
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
					s.eventsB.Store(count, line)
					count++
					// Forward exactly what we received (includes \n)
					io.WriteString(w, line)

					// Flush immediately for real-time streaming
					flusher.Flush()
				}
			}
		}),
	)

	s.srvC = app.New(t,
		app.WithHandlerFunc("/healthz", func(http.ResponseWriter, *http.Request) {}),
		app.WithHandlerFunc("/TOC", func(w http.ResponseWriter, r *http.Request) {
			url := fmt.Sprintf("http://%s/v1.0/invoke/%s/method/TOB", s.daprdC.HTTPAddress(), s.daprdB.AppID())
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

			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache, no-transform")
			w.Header().Set("Connection", "keep-alive")
			w.Header().Set("X-Accel-Buffering", "no")

			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
				return
			}

			count := 0
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
					s.eventsC.Store(count, line)
					count++
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
	s.daprdC = daprd.New(t, daprd.WithAppID("daprdC"), daprd.WithAppPort(s.srvC.Port()))

	return []framework.Option{
		framework.WithProcesses(s.srvA, s.srvB, s.srvC, s.daprdA, s.daprdB, s.daprdC),
	}
}

func (s *sserelay) Run(t *testing.T, ctx context.Context) {
	s.daprdA.WaitUntilRunning(t, ctx)
	s.daprdB.WaitUntilRunning(t, ctx)
	s.daprdC.WaitUntilRunning(t, ctx)

	client := client.HTTPWithTimeout(t, 60*time.Second)

	url := fmt.Sprintf("http://localhost:%d/TOC", s.srvC.Port())
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Accept-Encoding", "identity")

	resp, err := client.Do(req)
	if resp != nil {
		defer func() {
			cErr := resp.Body.Close()
			if cErr != nil {
				t.Log("Response body close error: ", cErr)
			}
		}()
	}
	require.NoError(t, err)

	// Verify SSE headers
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	// Use direct reader for real-time streaming
	done := make(chan struct{})
	errChan := make(chan error)
	eventCount := atomic.Int32{}
	go func() {
		defer close(done)
		reader := bufio.NewReader(resp.Body)
		for {
			line, rErr := reader.ReadString('\n')
			if rErr == io.EOF {
				return
			}
			if rErr != nil {
				if !errors.Is(rErr, context.Canceled) {
					errChan <- rErr
				}
				return
			}

			val, ok := s.eventsA.Load(int(eventCount.Load()))
			assert.True(t, ok)
			assert.Equal(t, val, line)

			val, ok = s.eventsB.Load(int(eventCount.Load()))
			assert.True(t, ok)
			assert.Equal(t, val, line)

			val, ok = s.eventsC.Load(int(eventCount.Load()))
			assert.True(t, ok)
			assert.Equal(t, val, line)

			eventCount.Add(1)
			// Stop after receiving a certain number of events for testing
			if eventCount.Load() > 5 {
				errChan <- errors.New("received too many events")
				return
			}
		}
	}()

	select {
	case <-done:
		assert.EqualValues(t, 5, eventCount.Load())

	case err = <-errChan:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		assert.Fail(t, "test timed out")
	}
}
