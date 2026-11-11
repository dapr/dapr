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

package restart

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/actors/activation"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(connection))
}

// connection checks a Connection header can't strip the activation id.
type connection struct {
	app *actors.Actors

	rec activation.Recorder
}

func (c *connection) Setup(t *testing.T) []framework.Option {
	c.app = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithHandler("/actors/abc/{id}/method/{method}", func(_ http.ResponseWriter, r *http.Request) {
			c.rec.Record("invoke", activation.FromHeader(r))
		}),
	)

	return []framework.Option{
		framework.WithProcesses(c.app),
	}
}

func (c *connection) Run(t *testing.T, ctx context.Context) {
	c.app.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)
	url := "http://" + c.app.Daprd().HTTPAddress() + "/v1.0/actors/abc/1/method/foo"
	for _, conn := range []string{"", activation.Header, "keep-alive, " + activation.Header} {
		require.EventuallyWithT(t, func(col *assert.CollectT) {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
			if !assert.NoError(col, err) {
				return
			}
			if conn != "" {
				req.Header.Set("Connection", conn)
			}
			resp, err := httpClient.Do(req)
			if !assert.NoError(col, err) {
				return
			}
			resp.Body.Close()
			assert.Equal(col, http.StatusOK, resp.StatusCode)
		}, time.Second*8, time.Millisecond*10)
	}

	calls := c.rec.Take()
	require.GreaterOrEqual(t, len(calls), 3)
	require.NotEmpty(t, calls[0].ID)
	for _, e := range calls {
		assert.Equal(t, calls[0].ID, e.ID)
	}
}
