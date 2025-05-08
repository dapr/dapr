/*
Copyright 2025 The Dapr Authors
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

package baggage

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(httpBaggage))
}

type httpBaggage struct {
	httpapp *prochttp.HTTP
	daprd   *daprd.Daprd

	baggage     atomic.Bool
	baggageVals atomic.Value
}

func (h *httpBaggage) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		if baggage := r.Header.Values("baggage"); len(baggage) > 0 {
			h.baggage.Store(true)
			h.baggageVals.Store(strings.Join(baggage, ","))
		} else {
			h.baggage.Store(false)
			h.baggageVals.Store("")
		}
		w.Write([]byte(`OK`))
	})

	h.httpapp = prochttp.New(t, prochttp.WithHandler(handler))
	h.daprd = daprd.New(t, daprd.WithAppPort(h.httpapp.Port()))

	return []framework.Option{
		framework.WithProcesses(h.httpapp, h.daprd),
	}
}

func (h *httpBaggage) Run(t *testing.T, ctx context.Context) {
	h.daprd.WaitUntilRunning(t, ctx)
	httpClient := client.HTTP(t)

	t.Run("no baggage header provided", func(t *testing.T) {
		// invoke app
		appURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/test", h.daprd.HTTPPort(), h.daprd.AppID())
		appreq, err := http.NewRequestWithContext(t.Context(), http.MethodPost, appURL, strings.NewReader("{\"operation\":\"get\"}"))
		require.NoError(t, err)
		appresp, err := httpClient.Do(appreq)
		require.NoError(t, err)
		defer appresp.Body.Close()
		assert.Equal(t, http.StatusOK, appresp.StatusCode)
		assert.False(t, h.baggage.Load())
		assert.Equal(t, "", h.baggageVals.Load())
	})

	t.Run("baggage header provided", func(t *testing.T) {
		// invoke app
		appURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/test", h.daprd.HTTPPort(), h.daprd.AppID())
		appreq, err := http.NewRequestWithContext(t.Context(), http.MethodPost, appURL, strings.NewReader("{\"operation\":\"get\"}"))
		require.NoError(t, err)

		baggageVal := "key1=value1,key2=value2"
		appreq.Header.Set("baggage", baggageVal)

		appresp, err := httpClient.Do(appreq)
		require.NoError(t, err)
		defer appresp.Body.Close()
		assert.Equal(t, http.StatusOK, appresp.StatusCode)
		assert.True(t, h.baggage.Load())
		assert.Equal(t, baggageVal, h.baggageVals.Load())

		// Verify baggage header is in response
		assert.Equal(t, baggageVal, appresp.Header.Get("baggage"))
	})

	t.Run("invalid baggage header", func(t *testing.T) {
		appURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/test", h.daprd.HTTPPort(), h.daprd.AppID())
		appreq, err := http.NewRequestWithContext(t.Context(), http.MethodPost, appURL, strings.NewReader("{\"operation\":\"get\"}"))
		require.NoError(t, err)

		appreq.Header.Set("baggage", "invalid-baggage")

		appresp, err := httpClient.Do(appreq)
		require.NoError(t, err)
		defer appresp.Body.Close()
		assert.Equal(t, http.StatusBadRequest, appresp.StatusCode)

		body, err := io.ReadAll(appresp.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), "invalid baggage header")
	})
}
