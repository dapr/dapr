/*
Copyright 2024 The Dapr Authors
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

package call

import (
	"context"
	"fmt"
	"io"
	nethttp "net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(errorheader))
}

type errorheader struct {
	app1 *actors.Actors
	app2 *actors.Actors
}

func (e *errorheader) Setup(t *testing.T) []framework.Option {
	e.app1 = actors.New(t)

	e.app2 = actors.New(t,
		actors.WithPeerActor(e.app1),
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			if r.Method == nethttp.MethodDelete {
				return
			}
			w.Header().Add("x-DaprErrorResponseHeader", "Simulated error header")
			w.Write([]byte("Simulated error response"))
		}),
	)

	return []framework.Option{
		framework.WithProcesses(e.app1, e.app2),
	}
}

func (e *errorheader) Run(t *testing.T, ctx context.Context) {
	e.app1.WaitUntilRunning(t, ctx)
	e.app2.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.ElementsMatch(t, []*daprd.MetadataActorRuntimeActiveActor{
			{
				Type:  "abc",
				Count: 0,
			},
		}, e.app2.Daprd().GetMetaActorRuntime(t, ctx).ActiveActors)
	}, time.Second*10, time.Millisecond*10)

	url := fmt.Sprintf("http://%s/v1.0/actors/abc/ii/method/foo", e.app1.Daprd().HTTPAddress())
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
	require.NoError(t, err)
	resp, err := client.HTTP(t).Do(req)
	require.NoError(t, err)

	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	assert.Equal(t, "Simulated error response", string(b))
	assert.Equal(t, 200, resp.StatusCode)

	gresp, err := e.app1.Daprd().GRPCClient(t, ctx).InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: "abc",
		ActorId:   "ii",
		Method:    "foo",
	})
	require.NoError(t, err)
	assert.Equal(t, "Simulated error response", string(gresp.GetData()))
}
