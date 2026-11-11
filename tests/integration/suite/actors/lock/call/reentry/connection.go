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

package reentry

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(connection))
}

// connection checks a Connection header can't strip the reentrancy id.
type connection struct {
	app *actors.Actors
	rid atomic.Pointer[string]
}

func (c *connection) Setup(t *testing.T) []framework.Option {
	httpClient := client.HTTP(t)

	c.app = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithReentry(true),
		actors.WithHandler("/actors/abc/{id}/method/outer", func(w http.ResponseWriter, r *http.Request) {
			rid := r.Header.Get("Dapr-Reentrancy-Id")
			c.rid.Store(&rid)

			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			url := "http://" + c.app.Daprd().HTTPAddress() + "/v1.0/actors/abc/" + r.PathValue("id") + "/method/inner"
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			req.Header.Set("Dapr-Reentrancy-Id", rid)
			resp, err := httpClient.Do(req)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			resp.Body.Close()
			w.WriteHeader(resp.StatusCode)
		}),
		actors.WithHandler("/actors/abc/{id}/method/inner", func(http.ResponseWriter, *http.Request) {}),
	)

	return []framework.Option{
		framework.WithProcesses(c.app),
	}
}

func (c *connection) Run(t *testing.T, ctx context.Context) {
	c.app.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)
	url := "http://" + c.app.Daprd().HTTPAddress() + "/v1.0/actors/abc/1/method/outer"
	require.EventuallyWithT(t, func(col *assert.CollectT) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
		if !assert.NoError(col, err) {
			return
		}
		req.Header.Set("Connection", "Dapr-Reentrancy-Id")
		resp, err := httpClient.Do(req)
		if !assert.NoError(col, err) {
			return
		}
		resp.Body.Close()
		assert.Equal(col, http.StatusOK, resp.StatusCode)
	}, time.Second*20, time.Millisecond*10)

	require.NotNil(t, c.rid.Load())
	assert.NotEmpty(t, *c.rid.Load())
}
