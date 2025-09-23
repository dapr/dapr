/*
Copyright 2025 The Dapr Authors
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

package http

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(redirect))
}

type redirect struct {
	daprd  *daprd.Daprd
	port   int
	called atomic.Bool
}

func (r *redirect) Setup(t *testing.T) []framework.Option {
	fp := ports.Reserve(t, 1)
	r.port = fp.Port(t)

	redirectHandler := func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, fmt.Sprintf("http://localhost:%d/helloworld", r.port), http.StatusMovedPermanently)
	}

	fp.Free(t)
	srv1 := prochttp.New(t,
		prochttp.WithPort(r.port),
		prochttp.WithHandlerFunc("/events", redirectHandler),
		prochttp.WithHandlerFunc("/helloworld", func(http.ResponseWriter, *http.Request) {
			r.called.Store(true)
		}),
	)

	r.daprd = daprd.New(t,
		daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: hello
spec:
  metrics:
    enabled: true
    rules: []
    latencyDistributionBuckets: []
    http:
      increasedCardinality: true
      pathMatching:
        - /events
        - /helloworld
      excludeVerbs: false
    recordErrorCodes: true
`),
	)

	return []framework.Option{
		framework.WithProcesses(fp, srv1, r.daprd),
	}
}

func (r *redirect) Run(t *testing.T, ctx context.Context) {
	r.daprd.WaitUntilRunning(t, ctx)

	url := fmt.Sprintf("http://localhost:%d/v1.0/invoke/http://localhost:%d/method/events", r.daprd.HTTPPort(), r.port)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)

	resp, err := client.HTTP(t).Do(req)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	assert.True(t, r.called.Load())
}
