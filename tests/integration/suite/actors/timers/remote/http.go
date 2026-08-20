/*
Copyright 2024 The Dapr Authors
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

package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	nethttp "net/http"
	"strconv"
	"strings"
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
	suite.Register(new(http))
}

type http struct {
	app1 *actors.Actors
	app2 *actors.Actors

	timer1, timer2 atomic.Int64
}

func (h *http) Setup(t *testing.T) []framework.Option {
	h.app1 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithHandler("/actors/abc/{id}", func(nethttp.ResponseWriter, *nethttp.Request) {}),
		actors.WithHandler("/actors/abc/{id}/method/timer/foo", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			assert.Equal(t, nethttp.MethodPut, r.Method)
			b, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			assert.JSONEq(t, `{"data":"hello","callback":"","dueTime":"0s","period":"1s"}`, string(b))
			h.timer1.Add(1)
		}),
	)

	h.app2 = actors.New(t,
		actors.WithPeerActor(h.app1),
		actors.WithActorTypes("abc"),
		actors.WithHandler("/actors/abc/{id}", func(nethttp.ResponseWriter, *nethttp.Request) {}),
		actors.WithHandler("/actors/abc/{id}/method/timer/foo", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			assert.Equal(t, nethttp.MethodPut, r.Method)
			b, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			assert.JSONEq(t, `{"data":"hello","callback":"","dueTime":"0s","period":"1s"}`, string(b))
			h.timer2.Add(1)
		}),
	)

	return []framework.Option{
		framework.WithProcesses(h.app1, h.app2),
	}
}

func (h *http) Run(t *testing.T, ctx context.Context) {
	h.app1.WaitUntilRunning(t, ctx)
	h.app2.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)
	body := `{"dueTime":"0s","period":"1s","data":"hello"}`

	do := func(t require.TestingT, method, addr, id string) int {
		var reqBody io.Reader
		if method == nethttp.MethodPost {
			reqBody = strings.NewReader(body)
		}
		url := fmt.Sprintf("http://%s/v1.0/actors/abc/%s/timers/foo", addr, id)
		req, err := nethttp.NewRequestWithContext(ctx, method, url, reqBody)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		if resp.StatusCode == nethttp.StatusForbidden {
			b, rerr := io.ReadAll(resp.Body)
			require.NoError(t, rerr)
			var apiErr struct {
				ErrorCode string `json:"errorCode"`
			}
			require.NoError(t, json.Unmarshal(b, &apiErr))
			assert.Equal(t, "ERR_ACTOR_TIMER_NOT_OWNED", apiErr.ErrorCode)
		}
		require.NoError(t, resp.Body.Close())
		return resp.StatusCode
	}

	owner1, owner2 := "", ""
	for i := 0; owner1 == "" || owner2 == ""; i++ {
		require.Less(t, i, 100, "actor IDs never hashed to both hosts")
		id := strconv.Itoa(i)
		code1 := do(t, nethttp.MethodPost, h.app1.Daprd().HTTPAddress(), id)
		code2 := do(t, nethttp.MethodPost, h.app2.Daprd().HTTPAddress(), id)
		codes := []int{code1, code2}
		assert.ElementsMatch(t, []int{nethttp.StatusNoContent, nethttp.StatusForbidden}, codes)
		if code1 == nethttp.StatusNoContent && owner1 == "" {
			owner1 = id
		}
		if code2 == nethttp.StatusNoContent && owner2 == "" {
			owner2 = id
		}
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Positive(c, h.timer1.Load())
		assert.Positive(c, h.timer2.Load())
	}, time.Second*10, time.Millisecond*10)

	assert.Equal(t, nethttp.StatusForbidden, do(t, nethttp.MethodDelete, h.app2.Daprd().HTTPAddress(), owner1))
	assert.Equal(t, nethttp.StatusForbidden, do(t, nethttp.MethodDelete, h.app1.Daprd().HTTPAddress(), owner2))
	assert.Equal(t, nethttp.StatusNoContent, do(t, nethttp.MethodDelete, h.app1.Daprd().HTTPAddress(), owner1))
	assert.Equal(t, nethttp.StatusNoContent, do(t, nethttp.MethodDelete, h.app2.Daprd().HTTPAddress(), owner2))
}
