/*
Copyright 2021 The Dapr Authors
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

package metrics

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/kit/logger"
)

func TestMetricsExporter(t *testing.T) {
	logger := logger.NewLogger("test.logger")

	t.Run("returns default options", func(t *testing.T) {
		e := New(Options{
			Enabled: DefaultFlagOptions().enabled,
			Port:    DefaultFlagOptions().port,
			Log:     logger,
			Healthz: healthz.New(),
		})
		assert.Equal(t, "9090", e.(*exporter).port)
		assert.True(t, e.(*exporter).enabled)
	})

	t.Run("skip starting metric server but wait for context cancellation", func(t *testing.T) {
		e := New(Options{
			Enabled: false,
			Port:    "9090",
			Log:     logger,
			Healthz: healthz.New(),
		})

		ctx, cancel := context.WithCancel(t.Context())
		errCh := make(chan error)
		go func() {
			errCh <- e.Start(ctx)
		}()

		cancel()

		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(time.Second):
			t.Error("expected metrics Run() to return in time when context is cancelled")
		}
	})

	t.Run("serves metrics only for GET and HEAD", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		port := ln.Addr().(*net.TCPAddr).Port
		require.NoError(t, ln.Close())

		e := New(Options{
			Enabled:       true,
			Port:          strconv.Itoa(port),
			ListenAddress: "127.0.0.1",
			Log:           logger,
			Healthz:       healthz.New(),
		})

		ctx, cancel := context.WithCancel(t.Context())
		errCh := make(chan error)
		go func() {
			errCh <- e.Start(ctx)
		}()
		t.Cleanup(func() {
			cancel()
			require.NoError(t, <-errCh)
		})

		url := "http://127.0.0.1:" + strconv.Itoa(port) + "/"
		client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
		do := func(method string) (int, string, error) {
			req, rerr := http.NewRequestWithContext(t.Context(), method, url, nil)
			require.NoError(t, rerr)
			resp, rerr := client.Do(req)
			if rerr != nil {
				return 0, "", rerr
			}
			defer resp.Body.Close()
			return resp.StatusCode, resp.Header.Get("Allow"), nil
		}

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			status, _, gerr := do(http.MethodGet)
			if assert.NoError(c, gerr) {
				assert.Equal(c, http.StatusOK, status)
			}
		}, 5*time.Second, 10*time.Millisecond)

		status, _, err := do(http.MethodHead)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, status)

		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			status, allow, err := do(method)
			require.NoError(t, err, method)
			assert.Equal(t, http.StatusMethodNotAllowed, status, method)
			assert.Equal(t, "GET, HEAD", allow, method)
		}
	})
}
