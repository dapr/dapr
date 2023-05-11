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
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(Healthz))
}

// Healthz tests that Dapr responds to healthz requests.
type Healthz struct {
	daprd *daprd.Daprd
}

func (h *Healthz) Setup(t *testing.T) []framework.Option {
	h.daprd = daprd.New(t)
	return []framework.Option{
		framework.WithProcesses(h.daprd),
	}
}

func (h *Healthz) Run(t *testing.T, ctx context.Context) {
	assert.Eventually(t, func() bool {
		conn, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", h.daprd.PublicPort))
		if err != nil {
			return false
		}
		require.NoError(t, conn.Close())
		return true
	}, time.Second*5, 100*time.Millisecond)

	reqURL := fmt.Sprintf("http://localhost:%d/v1.0/healthz", h.daprd.PublicPort)

	assert.Eventually(t, func() bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		require.NoError(t, err)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		return resp.StatusCode == http.StatusNoContent
	}, time.Second*10, 100*time.Millisecond)
}
