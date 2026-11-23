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

package metrics

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	procinjector "github.com/dapr/dapr/tests/integration/framework/process/injector"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	procoperator "github.com/dapr/dapr/tests/integration/framework/process/operator"
	procplacement "github.com/dapr/dapr/tests/integration/framework/process/placement"
	procscheduler "github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(methods))
}

type methods struct {
	daprd     *procdaprd.Daprd
	placement *procplacement.Placement
	scheduler *procscheduler.Scheduler
	sentry    *procsentry.Sentry
	injector  *procinjector.Injector
	operator  *procoperator.Operator
}

func (m *methods) Setup(t *testing.T) []framework.Option {
	m.sentry = procsentry.New(t,
		procsentry.WithNamespace("dapr-system"),
		procsentry.WithTrustDomain("integration.test.dapr.io"),
	)

	kubeAPI := kubernetes.New(t, kubernetes.WithBaseOperatorAPI(t,
		spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
		"dapr-system",
		m.sentry.Port(),
	))

	m.operator = procoperator.New(t,
		procoperator.WithNamespace("dapr-system"),
		procoperator.WithKubeconfigPath(kubeAPI.KubeconfigPath(t)),
		procoperator.WithTrustAnchorsFile(m.sentry.TrustAnchorsFile(t)),
	)
	m.injector = procinjector.New(t,
		procinjector.WithNamespace("dapr-system"),
		procinjector.WithSentry(m.sentry),
	)
	m.daprd = procdaprd.New(t)
	m.placement = procplacement.New(t)
	m.scheduler = procscheduler.New(t)

	return []framework.Option{
		framework.WithProcesses(kubeAPI, m.sentry, m.operator, m.injector,
			m.daprd, m.placement, m.scheduler),
	}
}

func (m *methods) Run(t *testing.T, ctx context.Context) {
	m.sentry.WaitUntilRunning(t, ctx)
	m.operator.WaitUntilRunning(t, ctx)
	m.injector.WaitUntilRunning(t, ctx)
	m.daprd.WaitUntilRunning(t, ctx)
	m.placement.WaitUntilRunning(t, ctx)
	m.scheduler.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	do := func(t *testing.T, method, url string) (int, http.Header, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader("foo"))
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		return resp.StatusCode, resp.Header, string(body)
	}

	for name, port := range map[string]int{
		"daprd":     m.daprd.MetricsPort(),
		"placement": m.placement.MetricsPort(),
		"scheduler": m.scheduler.MetricsPort(),
		"sentry":    m.sentry.MetricsPort(),
		"injector":  m.injector.MetricsPort(),
		"operator":  m.operator.MetricsPort(),
	} {
		t.Run(name, func(t *testing.T) {
			// The handler is registered on "/", so every path is served.
			// Prometheus scrapes "/metrics".
			for _, path := range []string{"/", "/metrics"} {
				url := fmt.Sprintf("http://127.0.0.1:%d%s", port, path)

				t.Run("GET "+path, func(t *testing.T) {
					status, header, body := do(t, http.MethodGet, url)
					assert.Equal(t, http.StatusOK, status)
					assert.Contains(t, header.Get("Content-Type"), "text/plain")
					assert.Contains(t, body, "go_goroutines")
				})

				t.Run("HEAD "+path, func(t *testing.T) {
					status, _, body := do(t, http.MethodHead, url)
					assert.Equal(t, http.StatusOK, status)
					assert.Empty(t, body)
				})

				for _, method := range []string{
					http.MethodPost,
					http.MethodPut,
					http.MethodPatch,
					http.MethodDelete,
					http.MethodOptions,
					http.MethodTrace,
				} {
					t.Run(method+" "+path, func(t *testing.T) {
						status, header, body := do(t, method, url)
						assert.Equal(t, http.StatusMethodNotAllowed, status)
						assert.Equal(t, "GET, HEAD", header.Get("Allow"))
						assert.NotContains(t, body, "go_goroutines")
					})
				}
			}
		})
	}
}
