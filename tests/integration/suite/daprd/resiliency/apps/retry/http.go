/*
Copyright 2024 The Dapr Authors
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

package retry

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

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	httpapp "github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	suite.Register(new(retryHTTP))
}

type retryHTTP struct {
	httpResiliency string
	counters       sync.Map

	handlerFuncRoot  httpapp.Option
	handlerFuncRetry httpapp.Option
}

func (rt *retryHTTP) Setup(t *testing.T) []framework.Option {
	rt.httpResiliency = `
apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: httpresiliency
spec:
  policies:
    retries:
      DefaultAppRetryPolicy:
        policy: constant
        duration: 1ms
        maxRetries: 2
        matching:
          httpStatusCodes: "%s"
`

	rt.handlerFuncRoot = httpapp.WithHandlerFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.Method))
	})

	rt.handlerFuncRetry = httpapp.WithHandlerFunc("/retry", func(w http.ResponseWriter, r *http.Request) {
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

		c, _ := rt.counters.LoadOrStore(key, &atomic.Int32{})
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

	return []framework.Option{}
}

func (rt *retryHTTP) Run(t *testing.T, ctx context.Context) {
	scenarios := []httpTestScenario{
		{
			title:           "No status codes",
			statusCodes:     "",
			statusCodesTest: []int{500},
			expectRetries:   true,
		},
		{
			title:           "Single status code no retries",
			statusCodes:     "200",
			statusCodesTest: []int{200},
			expectRetries:   false,
		},
		{
			title:           "Single status code with retries",
			statusCodes:     "500",
			statusCodesTest: []int{500},
			expectRetries:   true,
		},
		{
			title:           "Multiple status codes no retries",
			statusCodes:     "200,301",
			statusCodesTest: []int{200, 301},
			expectRetries:   false,
		},
		{
			title:           "Multiple status codes with retries",
			statusCodes:     "400,500",
			statusCodesTest: []int{400, 500},
			expectRetries:   true,
		},
		{
			title:           "Range success status codes no retries",
			statusCodes:     "200-320",
			statusCodesTest: []int{201, 301},
			expectRetries:   false,
		},
		{
			title:           "Range status codes with retries",
			statusCodes:     "400-550",
			statusCodesTest: []int{404, 501},
			expectRetries:   true,
		},
		{
			title:           "Multiple ranges status codes no retries",
			statusCodes:     "200,400-500,501",
			statusCodesTest: []int{200, 301, 399, 502},
			expectRetries:   false,
		},
		{
			title:           "Multiple ranges status codes with retries",
			statusCodes:     "200,400-500,501",
			statusCodesTest: []int{400, 401, 420, 500, 501},
			expectRetries:   true,
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.title, func(t *testing.T) {
			rt.runHTTPScenario(t, ctx, scenario)
		})
	}
}

type httpTestScenario struct {
	title           string
	statusCodes     string
	expectRetries   bool
	statusCodesTest []int
}

func (rt *retryHTTP) runHTTPScenario(t *testing.T, ctx context.Context, scenario httpTestScenario) {
	app1 := httpapp.New(t, rt.handlerFuncRoot, rt.handlerFuncRetry)
	app1.Run(t, ctx)

	daprd1 := daprd.New(t,
		daprd.WithAppPort(app1.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithResourceFiles(fmt.Sprintf(rt.httpResiliency, scenario.statusCodes)),
	)
	daprd1.Run(t, ctx)
	defer daprd1.Cleanup(t)

	app2 := httpapp.New(t, rt.handlerFuncRoot, rt.handlerFuncRetry)
	app2.Run(t, ctx)

	daprd2 := daprd.New(t,
		daprd.WithAppPort(app2.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithResourceFiles(fmt.Sprintf(rt.httpResiliency, scenario.statusCodes)),
	)
	daprd2.Run(t, ctx)
	defer daprd2.Cleanup(t)

	daprd1.WaitUntilRunning(t, ctx)
	daprd2.WaitUntilRunning(t, ctx)

	for _, statusCode := range scenario.statusCodesTest {
		key := uuid.NewString()
		statusCodeStr := strconv.Itoa(statusCode)

		reqURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/retry?key=%s", daprd1.HTTPPort(), daprd2.AppID(), key)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(statusCodeStr))
		require.NoError(t, err)
		resp, err := client.HTTP(t).Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		expectedCount := 1
		if scenario.expectRetries {
			// 3 = 1 try + 2 retries.
			expectedCount = 3
		}
		assert.Equal(t, expectedCount, rt.getCount(key), "Retry count mismatch for test case '%s' with codes %s and test code %d", scenario.title, scenario.statusCodes, statusCode)
	}
}

func (rt *retryHTTP) getCount(key string) int {
	c, ok := rt.counters.Load(key)
	if !ok {
		return 0
	}

	return int(c.(*atomic.Int32).Load())
}
