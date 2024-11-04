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

package timers

import (
	"context"
	"io"
	nethttp "net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(grpc))
}

type grpc struct {
	app          *actors.Actors
	called       atomic.Int64
	deactivating atomic.Bool
}

func (g *grpc) Setup(t *testing.T) []framework.Option {
	g.app = actors.New(t,
		actors.WithActorTypes("abc", "efg"),
		actors.WithActorTypeHandler("abc", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			if g.deactivating.Load() {
				assert.Equal(t, "/actors/abc/foo", r.URL.Path)
				assert.Equal(t, nethttp.MethodDelete, r.Method)
				return
			}
			assert.Equal(t, nethttp.MethodPut, r.Method)
			if g.called.Add(1) == 1 {
				assert.Equal(t, "/actors/abc/foo/method/foo", r.URL.Path)
			} else {
				assert.Equal(t, "/actors/abc/foo/method/timer/foo", r.URL.Path)
				b, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				assert.Equal(t, `{"data":"aGVsbG8=","callback":"","dueTime":"0s","period":"1s"}`, string(b))
			}
		}),
	)

	return []framework.Option{
		framework.WithProcesses(g.app),
	}
}

func (g *grpc) Run(t *testing.T, ctx context.Context) {
	g.app.WaitUntilRunning(t, ctx)

	t.Cleanup(func() { g.deactivating.Store(true) })

	client := g.app.Daprd().GRPCClient(t, ctx)

	_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: "abc",
		ActorId:   "foo",
		Method:    "foo",
	})
	require.NoError(t, err)

	_, err = client.RegisterActorTimer(ctx, &rtv1.RegisterActorTimerRequest{
		ActorType: "abc",
		ActorId:   "foo",
		Name:      "foo",
		DueTime:   "0s",
		Period:    "1s",
		Data:      []byte("hello"),
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(t, g.called.Load(), int64(2))
	}, time.Second*10, time.Millisecond*10)

	_, err = client.UnregisterActorTimer(ctx, &rtv1.UnregisterActorTimerRequest{
		ActorType: "abc",
		ActorId:   "foo",
		Name:      "foo",
	})
	require.NoError(t, err)

	called := g.called.Load()
	time.Sleep(time.Second * 2)
	assert.Equal(t, called, g.called.Load())
}
