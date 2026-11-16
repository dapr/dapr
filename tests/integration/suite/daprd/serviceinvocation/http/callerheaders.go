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
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(callerheaders))
}

type callerheaders struct {
	sentry      *sentry.Sentry
	mtlsCaller  *daprd.Daprd
	mtlsCallee  *daprd.Daprd
	plainCaller *daprd.Daprd
	plainCallee *daprd.Daprd
	ch          chan http.Header
}

func (c *callerheaders) Setup(t *testing.T) []framework.Option {
	c.ch = make(chan http.Header, 1)
	app := app.New(t,
		app.WithHandlerFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
			c.ch <- r.Header
		}),
	)

	c.sentry = sentry.New(t)
	c.mtlsCaller = daprd.New(t, daprd.WithSentry(t, c.sentry))
	c.mtlsCallee = daprd.New(t,
		daprd.WithSentry(t, c.sentry),
		daprd.WithAppPort(app.Port()),
	)
	c.plainCaller = daprd.New(t)
	c.plainCallee = daprd.New(t, daprd.WithAppPort(app.Port()))

	return []framework.Option{
		framework.WithProcesses(app, c.sentry, c.mtlsCaller, c.mtlsCallee, c.plainCaller, c.plainCallee),
	}
}

func (c *callerheaders) Run(t *testing.T, ctx context.Context) {
	c.sentry.WaitUntilRunning(t, ctx)
	for _, d := range []*daprd.Daprd{c.mtlsCaller, c.mtlsCallee, c.plainCaller, c.plainCallee} {
		d.WaitUntilRunning(t, ctx)
	}
	c.mtlsCallee.WaitUntilAppHealth(t, ctx)
	c.plainCallee.WaitUntilAppHealth(t, ctx)

	httpClient := client.HTTP(t)

	for name, pair := range map[string]struct{ caller, callee *daprd.Daprd }{
		"mtls":    {c.mtlsCaller, c.mtlsCallee},
		"no mtls": {c.plainCaller, c.plainCallee},
	} {
		t.Run(name, func(t *testing.T) {
			reqURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/hello", pair.caller.HTTPPort(), pair.callee.AppID())

			invoke := func(t *testing.T, headers map[string]string) http.Header {
				t.Helper()
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
				require.NoError(t, err)
				for k, v := range headers {
					// Set the raw key so the casing on the wire is exactly k.
					req.Header[k] = []string{v}
				}
				resp, err := httpClient.Do(req)
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
				require.Equal(t, http.StatusOK, resp.StatusCode)

				select {
				case header := <-c.ch:
					return header
				case <-time.After(10 * time.Second):
					require.Fail(t, "timed out waiting for app to receive request")
					return nil
				}
			}

			assertIdentity := func(t *testing.T, header http.Header) {
				t.Helper()
				assert.Equal(t, []string{pair.caller.AppID()}, header.Values("Dapr-Caller-App-Id"))
				assert.Equal(t, []string{pair.caller.Namespace()}, header.Values("Dapr-Caller-Namespace"))
				assert.Equal(t, []string{pair.callee.AppID()}, header.Values("Dapr-Callee-App-Id"))
			}

			t.Run("no identity headers sent", func(t *testing.T) {
				assertIdentity(t, invoke(t, nil))
			})

			t.Run("spoofed identity headers in canonical casing", func(t *testing.T) {
				assertIdentity(t, invoke(t, map[string]string{
					"Dapr-Caller-App-Id":    "admin",
					"Dapr-Caller-Namespace": "kube-system",
					"Dapr-Callee-App-Id":    "other",
				}))
			})

			t.Run("spoofed identity headers in lowercase", func(t *testing.T) {
				assertIdentity(t, invoke(t, map[string]string{
					"dapr-caller-app-id":    "admin",
					"dapr-caller-namespace": "kube-system",
					"dapr-callee-app-id":    "other",
				}))
			})
		})
	}
}
