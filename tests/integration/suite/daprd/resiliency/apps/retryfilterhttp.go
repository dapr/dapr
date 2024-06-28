/*
Copyright 2024 The Dapr Authors
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

package apps

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(retryfilterhttp))
}

type retryfilterhttp struct {
	daprd1   *daprd.Daprd
	daprd2   *daprd.Daprd
	counters sync.Map
}

func (d *retryfilterhttp) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.Method))
	})

	handler.HandleFunc("/retry", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		key := r.URL.Query().Get("key")
		if key == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		c, _ := d.counters.LoadOrStore(key, &atomic.Int32{})
		counter := c.(*atomic.Int32)
		counter.Add(1)

		respStatusCode, err := strconv.Atoi(string(body))
		if (err != nil) || (respStatusCode < 100) || (respStatusCode >= 600) {
			// Trying to write a bad status code forces a 500 anyway.
			// So we simply pick one status code for these cases.
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		w.Header().Set("content-type", "text/plain")
		w.WriteHeader(respStatusCode)
		w.Write([]byte{})
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))

	resiliency := `
apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: myresiliency
spec:
  policies:
    retries:
      DefaultAppRetryPolicy:
        policy: constant
        duration: 100ms
        maxRetries: 3
        retryOnCodes: "505,2,501-503,509,546,547"
`
	d.daprd1 = daprd.New(t,
		daprd.WithAppPort(srv.Port()),
		daprd.WithResourceFiles(resiliency),
	)
	d.daprd2 = daprd.New(t,
		daprd.WithAppPort(srv.Port()),
		daprd.WithResourceFiles(resiliency),
	)
	d.counters = sync.Map{}

	return []framework.Option{
		framework.WithProcesses(srv, d.daprd1, d.daprd2),
	}
}

func (d *retryfilterhttp) getCount(key string) int {
	c, ok := d.counters.Load(key)
	if !ok {
		return 0
	}

	return int(c.(*atomic.Int32).Load())
}

func (d *retryfilterhttp) Run(t *testing.T, ctx context.Context) {
	d.daprd1.WaitUntilRunning(t, ctx)
	d.daprd2.WaitUntilRunning(t, ctx)

	scenarios := []struct {
		title              string
		statusCode         int
		expectedStatusCode int
		expectRetries      bool
	}{
		{
			title:              "success status not in filter list",
			statusCode:         200,
			expectedStatusCode: 200,
			expectRetries:      false,
		},
		{
			title:              "error status not in filter list",
			statusCode:         403,
			expectedStatusCode: 403,
			expectRetries:      false,
		},
		{
			title:              "invalid status not in filter list",
			statusCode:         0,
			expectedStatusCode: 400, // this tests's default error
			expectRetries:      false,
		},
		{
			title:              "invalid status in filter list",
			statusCode:         2,
			expectedStatusCode: 400, // this tests's default error
			expectRetries:      false,
		},
		{
			title:              "status in filter list",
			statusCode:         505,
			expectedStatusCode: 505,
			expectRetries:      true,
		},
		{
			title:              "status in filter list 501",
			statusCode:         501,
			expectedStatusCode: 501,
			expectRetries:      true,
		},
		{
			title:              "status in filter list 502",
			statusCode:         502,
			expectedStatusCode: 502,
			expectRetries:      true,
		},
		{
			title:              "status in filter list 503",
			statusCode:         503,
			expectedStatusCode: 503,
			expectRetries:      true,
		},
		{
			title:              "status not in filter list 504",
			statusCode:         504,
			expectedStatusCode: 504,
			expectRetries:      false,
		},
		{
			title:              "status in filter list 509",
			statusCode:         509,
			expectedStatusCode: 509,
			expectRetries:      true,
		},
		{
			title:              "status in filter list 546",
			statusCode:         546,
			expectedStatusCode: 546,
			expectRetries:      true,
		},
		{
			title:              "status in filter list 547",
			statusCode:         547,
			expectedStatusCode: 547,
			expectRetries:      true,
		},
		{
			title:              "status not in filter list 548",
			statusCode:         548,
			expectedStatusCode: 548,
			expectRetries:      false,
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.title, func(t *testing.T) {
			key := uuid.NewString()
			statusCodeStr := strconv.Itoa(scenario.statusCode)

			reqURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/retry?key=%s", d.daprd1.HTTPPort(), d.daprd2.AppID(), key)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(statusCodeStr))
			require.NoError(t, err)
			resp, err := util.HTTPClient(t).Do(req)
			require.NoError(t, err)

			assert.Equal(t, scenario.expectedStatusCode, resp.StatusCode)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())

			expectedCount := 1
			if scenario.expectRetries {
				// 4 = 1 try + 3 retries.
				expectedCount = 4
			}
			assert.Equal(t, expectedCount, d.getCount(key))
		})
	}
}