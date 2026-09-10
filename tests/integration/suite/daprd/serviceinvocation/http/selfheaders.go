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

package http

import (
	"context"
	"fmt"
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
	suite.Register(new(selfheaders))
}

// selfheaders asserts that a self-invocation (invoking one's own app ID, which
// short-circuits to invokeLocal) stamps the caller/callee identity headers the
// same way a cross-app invocation does, and that a caller cannot spoof those
// headers on the local leg.
type selfheaders struct {
	daprd *daprd.Daprd
	ch    chan http.Header
}

func (s *selfheaders) Setup(t *testing.T) []framework.Option {
	s.ch = make(chan http.Header, 1)
	app := app.New(t,
		app.WithHandlerFunc("/helloworld", func(w http.ResponseWriter, r *http.Request) {
			s.ch <- r.Header
		}),
	)

	s.daprd = daprd.New(t,
		daprd.WithAppID("app-self"),
		daprd.WithNamespace("app-self-namespace"),
		daprd.WithAppPort(app.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(app, s.daprd),
	}
}

func (s *selfheaders) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilAppHealth(t, ctx)

	httpClient := client.HTTP(t)
	reqURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/helloworld", s.daprd.HTTPPort(), s.daprd.AppID())

	invoke := func(t *testing.T, headers map[string]string) http.Header {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		require.NoError(t, err)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusOK, resp.StatusCode)

		select {
		case header := <-s.ch:
			return header
		case <-time.After(10 * time.Second):
			assert.Fail(t, "timed out waiting for app to receive request")
			return nil
		}
	}

	t.Run("self-invocation stamps caller and callee headers", func(t *testing.T) {
		header := invoke(t, nil)
		assert.Equal(t, "app-self-namespace", header.Get("dapr-caller-namespace"))
		assert.Equal(t, "app-self", header.Get("dapr-caller-app-id"))
		assert.Equal(t, "app-self", header.Get("dapr-callee-app-id"))
	})

	t.Run("self-invocation overwrites spoofed caller and callee headers", func(t *testing.T) {
		header := invoke(t, map[string]string{
			"dapr-caller-namespace": "spoofed-namespace",
			"dapr-caller-app-id":    "spoofed-app",
			"dapr-callee-app-id":    "spoofed-callee",
		})
		assert.Equal(t, "app-self-namespace", header.Get("dapr-caller-namespace"))
		assert.Equal(t, "app-self", header.Get("dapr-caller-app-id"))
		assert.Equal(t, "app-self", header.Get("dapr-callee-app-id"))
	})
}
