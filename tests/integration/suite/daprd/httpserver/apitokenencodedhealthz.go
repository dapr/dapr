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

package httpserver

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(apitokenencodedhealthz))
}

// apitokenencodedhealthz verifies that percent-encoded variants of the healthz
// path are not exempt from API token auth, as they do not route to healthz.
type apitokenencodedhealthz struct {
	daprd   *procdaprd.Daprd
	appHits atomic.Int64
}

func (a *apitokenencodedhealthz) Setup(t *testing.T) []framework.Option {
	app := prochttp.New(t, prochttp.WithHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Ignore the /dapr/config and /dapr/subscribe calls daprd makes on startup.
		if !strings.HasPrefix(r.URL.Path, "/dapr/") {
			a.appHits.Add(1)
		}
	})))

	a.daprd = procdaprd.New(t,
		procdaprd.WithDaprAPIToken(t, "test"),
		procdaprd.WithAppPort(app.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(app, a.daprd),
	}
}

func (a *apitokenencodedhealthz) Run(t *testing.T, ctx context.Context) {
	a.daprd.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	for _, p := range []string{"/v1.0/healt%68z", "/v1.0%2Fhealthz"} {
		t.Run(p, func(t *testing.T) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://localhost:%d%s", a.daprd.HTTPPort(), p), nil)
			require.NoError(t, err)
			require.Equal(t, p, req.URL.EscapedPath())
			req.Header.Set("dapr-app-id", a.daprd.AppID())

			//nolint:bodyclose
			res, err := httpClient.Do(req)
			require.NoError(t, err)
			defer closeBody(res.Body)
			assert.Equal(t, http.StatusUnauthorized, res.StatusCode)
			assert.Equal(t, int64(0), a.appHits.Load())
		})
	}

	t.Run("healthz without token", func(t *testing.T) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://localhost:%d/v1.0/healthz", a.daprd.HTTPPort()), nil)
		require.NoError(t, err)

		//nolint:bodyclose
		res, err := httpClient.Do(req)
		require.NoError(t, err)
		defer closeBody(res.Body)
		assert.Equal(t, http.StatusNoContent, res.StatusCode)
	})
}
