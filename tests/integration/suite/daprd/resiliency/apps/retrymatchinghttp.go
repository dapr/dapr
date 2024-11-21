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
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(retrymatchinghttp))
}

type retrymatchinghttp struct {
	daprd1   *daprd.Daprd
	daprd2   *daprd.Daprd
	counters sync.Map
}

func (d *retrymatchinghttp) Setup(t *testing.T) []framework.Option {
	handlerFuncRoot := app.WithHandlerFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.Method))
	})
	handlerFuncRetry := app.WithHandlerFunc("/retry", func(w http.ResponseWriter, r *http.Request) {
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
		if err != nil {
			respStatusCode = 204
		}

		w.Header().Set("content-type", "text/plain")
		w.WriteHeader(respStatusCode)
		w.Write([]byte{})
	})
	app1 := app.New(t, handlerFuncRoot, handlerFuncRetry)
	app2 := app.New(t, handlerFuncRoot, handlerFuncRetry)

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
        duration: 10ms
        maxRetries: 3
        matching:
          httpStatusCodes: "100,101,102,200,304,403,505,501-503,509,546,547"
          gRPCStatusCodes: "1-5"
`

	d.daprd1 = daprd.New(t,
		daprd.WithAppPort(app1.Port()),
		daprd.WithResourceFiles(resiliency),
	)
	d.daprd2 = daprd.New(t,
		daprd.WithAppPort(app2.Port()),
		daprd.WithResourceFiles(resiliency),
	)
	d.counters = sync.Map{}

	return []framework.Option{
		framework.WithProcesses(app1, app2, d.daprd1, d.daprd2),
	}
}

func (d *retrymatchinghttp) getCount(key string) int {
	c, ok := d.counters.Load(key)
	if !ok {
		return 0
	}

	return int(c.(*atomic.Int32).Load())
}

func (d *retrymatchinghttp) Run(t *testing.T, ctx context.Context) {
	d.daprd1.WaitUntilRunning(t, ctx)
	d.daprd2.WaitUntilRunning(t, ctx)

	scenarios := []struct {
		title              string
		statusCode         int
		expectedStatusCode int
		expectRetries      bool
	}{
		{
			title:              "success status in matching list",
			statusCode:         200,
			expectedStatusCode: 200,
			expectRetries:      false,
		},
		{
			title:              "success status not in matching list",
			statusCode:         204,
			expectedStatusCode: 204,
			expectRetries:      false,
		},
		{
			title:              "error status not in matching list 404",
			statusCode:         404,
			expectedStatusCode: 404,
			expectRetries:      false,
		},
		{
			title:              "status in matching list 100",
			statusCode:         100,
			expectedStatusCode: 200,
			expectRetries:      false, // success status code
		},
		{
			title:              "status in matching list 101",
			statusCode:         101,
			expectedStatusCode: 101,
			expectRetries:      true,
		},
		{
			title:              "status in matching list 102",
			statusCode:         102,
			expectedStatusCode: 200,
			expectRetries:      false, // not considered an error
		},
		{
			title:              "status in matching list 304",
			statusCode:         304,
			expectedStatusCode: 304,
			expectRetries:      false, // not considered an error
		},
		{
			title:              "status in matching list 505",
			statusCode:         505,
			expectedStatusCode: 505,
			expectRetries:      true,
		},
		{
			title:              "status in matching list 403",
			statusCode:         403,
			expectedStatusCode: 403,
			expectRetries:      true,
		},
		{
			title:              "status in matching list 501",
			statusCode:         501,
			expectedStatusCode: 501,
			expectRetries:      true,
		},
		{
			title:              "status in matching list 502",
			statusCode:         502,
			expectedStatusCode: 502,
			expectRetries:      true,
		},
		{
			title:              "status in matching list 503",
			statusCode:         503,
			expectedStatusCode: 503,
			expectRetries:      true,
		},
		{
			title:              "status not in matching list 504",
			statusCode:         504,
			expectedStatusCode: 504,
			expectRetries:      false,
		},
		{
			title:              "status in matching list 509",
			statusCode:         509,
			expectedStatusCode: 509,
			expectRetries:      true,
		},
		{
			title:              "status in matching list 546",
			statusCode:         546,
			expectedStatusCode: 546,
			expectRetries:      true,
		},
		{
			title:              "status in matching list 547",
			statusCode:         547,
			expectedStatusCode: 547,
			expectRetries:      true,
		},
		{
			title:              "status not in matching list 548",
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
			resp, err := client.HTTP(t).Do(req)
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
