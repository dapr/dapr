/*
Copyright 2023 The Dapr Authors
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

package healthz

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
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(daprd))
}

// daprd tests that Dapr responds to healthz requests.
type daprd struct {
	proc     *procdaprd.Daprd
	procmtls *procdaprd.Daprd
	sentry   *procsentry.Sentry
}

func (d *daprd) Setup(t *testing.T) []framework.Option {
	d.sentry = procsentry.New(t)
	d.proc = procdaprd.New(t)
	d.procmtls = procdaprd.New(t, procdaprd.WithSentry(t, d.sentry))
	return []framework.Option{
		framework.WithProcesses(d.proc, d.procmtls),
	}
}

func (d *daprd) Run(t *testing.T, ctx context.Context) {
	client := client.HTTP(t)

	t.Run("default", func(t *testing.T) {
		d.proc.WaitUntilTCPReady(t, ctx)

		for _, port := range []int{d.proc.PublicPort(), d.proc.HTTPPort()} {
			reqURL := fmt.Sprintf("http://localhost:%d/v1.0/healthz", port)
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
			require.NoError(t, err)

			assert.EventuallyWithT(t, func(c *assert.CollectT) {
				resp, err := client.Do(req)
				if assert.NoError(c, err) {
					require.NoError(t, resp.Body.Close())
					assert.Equal(c, http.StatusNoContent, resp.StatusCode)
				}
			}, time.Second*10, 10*time.Millisecond)
		}
	})

	t.Run("mtls", func(t *testing.T) {
		for _, port := range []int{d.procmtls.PublicPort(), d.procmtls.HTTPPort()} {
			reqURL := fmt.Sprintf("http://localhost:%d/v1.0/healthz", port)
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
			require.NoError(t, err)
			//nolint:bodyclose
			_, err = client.Do(req)
			require.Error(t, err)
			assert.Regexp(t, "No connection|connection refused", err.Error())
		}

		d.sentry.Run(t, ctx)
		t.Cleanup(func() { d.sentry.Cleanup(t) })

		for _, port := range []int{d.procmtls.PublicPort(), d.procmtls.HTTPPort()} {
			reqURL := fmt.Sprintf("http://localhost:%d/v1.0/healthz", port)
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
			require.NoError(t, err)

			assert.EventuallyWithT(t, func(t *assert.CollectT) {
				resp, err := client.Do(req)
				if assert.NoError(t, err) {
					_ = assert.NoError(t, resp.Body.Close()) &&
						assert.Equal(t, http.StatusNoContent, resp.StatusCode)
				}
			}, time.Second*10, 10*time.Millisecond)
		}
	})
}
