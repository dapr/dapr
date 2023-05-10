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
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(Healthz))
}

// Healthz tests that Dapr responds to healthz requests.
type Healthz struct{}

func (h *Healthz) Setup(t *testing.T, _ context.Context) []framework.RunDaprdOption {
	return nil
}

func (h *Healthz) Run(t *testing.T, _ context.Context, cmd *framework.Command) {
	assert.Eventually(t, func() bool {
		_, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", cmd.PublicPort))
		return err == nil
	}, time.Second*5, time.Millisecond)

	var resp *http.Response
	var err error
	assert.Eventually(t, func() bool {
		resp, err = http.DefaultClient.Get(fmt.Sprintf("http://localhost:%d/v1.0/healthz", cmd.PublicPort))
		require.NoError(t, err)
		return http.StatusNoContent == resp.StatusCode
	}, time.Second*5, time.Millisecond)
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Empty(t, b)
}
