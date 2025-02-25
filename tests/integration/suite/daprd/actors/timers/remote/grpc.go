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

package remote

import (
	"context"
	"io"
	nethttp "net/http"
	"strconv"
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
	app1 *actors.Actors
	app2 *actors.Actors

	called1, called2 atomic.Int64
	timer1, timer2   atomic.Int64
}

func (g *grpc) Setup(t *testing.T) []framework.Option {
	g.app1 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithHandler("/actors/abc/{id}", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			assert.Regexp(t, "/actors/abc/.+", r.URL.Path)
			assert.Equal(t, nethttp.MethodDelete, r.Method)
		}),
		actors.WithHandler("/actors/abc/{id}/method/foo", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			assert.Equal(t, nethttp.MethodPut, r.Method)
			assert.Regexp(t, "/actors/abc/.+/method/foo", r.URL.Path)
			g.called1.Add(1)
		}),
		actors.WithHandler("/actors/abc/{id}/method/timer/foo", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			assert.Equal(t, nethttp.MethodPut, r.Method)
			assert.Regexp(t, "/actors/abc/.+/method/timer/foo", r.URL.Path)
			b, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			assert.Equal(t, `{"data":"aGVsbG8=","callback":"","dueTime":"0s","period":"1s"}`, string(b))
			g.timer1.Add(1)
		}),
	)

	g.app2 = actors.New(t,
		actors.WithPeerActor(g.app1),
		actors.WithActorTypes("abc"),
		actors.WithHandler("/actors/abc/{id}", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			assert.Regexp(t, "/actors/abc/.+", r.URL.Path)
			assert.Equal(t, nethttp.MethodDelete, r.Method)
		}),
		actors.WithHandler("/actors/abc/{id}/method/foo", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			assert.Equal(t, nethttp.MethodPut, r.Method)
			assert.Regexp(t, "/actors/abc/.+", r.URL.Path)
			g.called2.Add(1)
		}),
		actors.WithHandler("/actors/abc/{id}/method/timer/foo", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			assert.Equal(t, nethttp.MethodPut, r.Method)
			assert.Regexp(t, "/actors/abc/.+/method/timer/foo", r.URL.Path)
			b, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			assert.Equal(t, `{"data":"aGVsbG8=","callback":"","dueTime":"0s","period":"1s"}`, string(b))
			g.timer2.Add(1)
		}),
	)

	return []framework.Option{
		framework.WithProcesses(g.app1, g.app2),
	}
}

func (g *grpc) Run(t *testing.T, ctx context.Context) {
	g.app1.WaitUntilRunning(t, ctx)
	g.app2.WaitUntilRunning(t, ctx)

	client := g.app1.Daprd().GRPCClient(t, ctx)

	var i atomic.Int64
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   strconv.Itoa(int(i.Add(1))),
			Method:    "foo",
		})
		require.NoError(t, err)
		_, err = client.RegisterActorTimer(ctx, &rtv1.RegisterActorTimerRequest{
			ActorType: "abc",
			ActorId:   strconv.Itoa(int(i.Load())),
			Name:      "foo",
			DueTime:   "0s",
			Period:    "1s",
			Data:      []byte("hello"),
		})
		require.NoError(t, err)

		assert.Positive(c, g.called1.Load())
		assert.Positive(c, g.called2.Load())
	}, time.Second*10, 1)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, g.timer1.Load(), int64(2))
		assert.GreaterOrEqual(c, g.timer2.Load(), int64(2))
	}, time.Second*10, time.Millisecond*10)
}
