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

package sighup

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(httpendpointsighup))
}

// httpendpointsighup tests that HTTP endpoint resources are reloaded when
// daprd receives a SIGHUP signal in selfhosted mode.
type httpendpointsighup struct {
	daprd       *daprd.Daprd
	endpointDir string
}

func (h *httpendpointsighup) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	h.endpointDir = t.TempDir()

	configFile := os.WriteFileYaml(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: sighup-httpendpoint
spec: {}
`)

	h.daprd = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(h.endpointDir),
	)

	return []framework.Option{
		framework.WithProcesses(h.daprd),
	}
}

func (h *httpendpointsighup) Run(t *testing.T, ctx context.Context) {
	h.daprd.WaitUntilRunning(t, ctx)

	t.Run("no HTTP endpoints initially", func(t *testing.T) {
		assert.Empty(t, h.daprd.GetMetaHTTPEndpoints(t, ctx))
	})

	t.Run("HTTP endpoint loaded after SIGHUP", func(t *testing.T) {
		os.WriteFileTo(t, h.endpointDir+"/endpoint.yaml", `
apiVersion: dapr.io/v1alpha1
kind: HTTPEndpoint
metadata:
  name: myendpoint
spec:
  baseUrl: http://localhost:1234
`)

		h.daprd.SignalHUP(t)
		h.daprd.WaitUntilRunning(t, ctx)

		require.EventuallyWithT(t, func(ct *assert.CollectT) {
			endpoints := h.daprd.GetMetaHTTPEndpoints(ct, ctx)
			assert.Len(ct, endpoints, 1)
		}, 10*time.Second, 10*time.Millisecond)

		assert.ElementsMatch(t, []*rtv1.MetadataHTTPEndpoint{
			{Name: "myendpoint"},
		}, h.daprd.GetMetaHTTPEndpoints(t, ctx))
	})

	t.Run("HTTP endpoint removed after SIGHUP", func(t *testing.T) {
		os.WriteFileTo(t, h.endpointDir+"/endpoint.yaml", `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: placeholder
spec: {}
`)

		h.daprd.SignalHUP(t)
		h.daprd.WaitUntilRunning(t, ctx)

		require.EventuallyWithT(t, func(ct *assert.CollectT) {
			endpoints := h.daprd.GetMetaHTTPEndpoints(ct, ctx)
			assert.Empty(ct, endpoints)
		}, 10*time.Second, 10*time.Millisecond)
	})
}
