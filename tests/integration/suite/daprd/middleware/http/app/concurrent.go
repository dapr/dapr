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

package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
	suite.Register(new(concurrent))
}

// concurrent sends overlapping invocations to an HTTP app, half of which the
// app fails by dropping the connection, and checks every request gets its own
// outcome, with and without an app HTTP middleware pipeline.
type concurrent struct {
	plain    *daprd.Daprd
	pipeline *daprd.Daprd
}

func (c *concurrent) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/fail") {
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				conn.Close()
			}
			return
		}
		io.WriteString(w, r.URL.Path)
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: uppercase
spec:
  appHttpPipeline:
    handlers:
      - name: uppercase
        type: middleware.http.uppercase
`), 0o600))

	c.plain = daprd.New(t, daprd.WithAppPort(srv.Port()))
	c.pipeline = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: uppercase
spec:
  type: middleware.http.uppercase
  version: v1
`),
		daprd.WithAppPort(srv.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(srv, c.plain, c.pipeline),
	}
}

// method fails even-numbered requests in the app.
func method(i int) string {
	if i%2 == 0 {
		return "fail" + strconv.Itoa(i)
	}
	return "ok" + strconv.Itoa(i)
}

func (c *concurrent) Run(t *testing.T, ctx context.Context) {
	c.plain.WaitUntilRunning(t, ctx)
	c.pipeline.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	for name, d := range map[string]*daprd.Daprd{"no pipeline": c.plain, "pipeline": c.pipeline} {
		t.Run(name, func(t *testing.T) {
			for range 10 {
				type outcome struct {
					status int
					body   string
					err    error
				}
				outcomes := make([]outcome, 32)

				var wg sync.WaitGroup
				for i := range outcomes {
					wg.Go(func() {
						url := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/%s", d.HTTPPort(), d.AppID(), method(i))
						req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
						if err != nil {
							outcomes[i].err = err
							return
						}
						resp, err := httpClient.Do(req)
						if err != nil {
							outcomes[i].err = err
							return
						}
						defer resp.Body.Close()
						body, err := io.ReadAll(resp.Body)
						outcomes[i] = outcome{status: resp.StatusCode, body: string(body), err: err}
					})
				}
				wg.Wait()

				for i, o := range outcomes {
					m := method(i)
					require.NoError(t, o.err, m)
					if i%2 == 0 {
						assert.NotEqual(t, http.StatusOK, o.status, "%s must fail, got %q", m, o.body)
						continue
					}
					assert.Equal(t, http.StatusOK, o.status, "%s must succeed, got %q", m, o.body)
					assert.Equal(t, "/"+m, o.body, "%s got another request's response", m)
				}
			}
		})
	}
}
