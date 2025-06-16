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

package chain

import (
	"context"
	nethttp "net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/concurrency/slice"
)

func init() {
	suite.Register(new(grpc))
}

type grpc struct {
	app      *actors.Actors
	called   slice.Slice[string]
	holdCall chan struct{}
}

func (g *grpc) Setup(t *testing.T) []framework.Option {
	g.called = slice.New[string]()
	g.holdCall = make(chan struct{})

	g.app = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			if r.Method == nethttp.MethodDelete {
				return
			}
			g.called.Append(r.URL.Path)
			<-g.holdCall
		}),
	)
	return []framework.Option{
		framework.WithProcesses(g.app),
	}
}

func (g *grpc) Run(t *testing.T, ctx context.Context) {
	g.app.WaitUntilRunning(t, ctx)

	client := g.app.GRPCClient(t, ctx)
	errCh := make(chan error)
	go func() {
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   "123",
			Method:    "foo",
		})
		errCh <- err
	}()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, []string{"/actors/abc/123/method/foo"}, g.called.Slice())
	}, time.Second*10, time.Millisecond*10)

	go func() {
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   "456",
			Method:    "foo",
		})
		errCh <- err
	}()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, []string{
			"/actors/abc/123/method/foo",
			"/actors/abc/456/method/foo",
		}, g.called.Slice())
	}, time.Second*10, time.Millisecond*10)

	go func() {
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   "123",
			Method:    "foo",
		})
		errCh <- err
	}()

	time.Sleep(time.Second)
	assert.Equal(t, []string{
		"/actors/abc/123/method/foo",
		"/actors/abc/456/method/foo",
	}, g.called.Slice())

	g.holdCall <- struct{}{}
	g.holdCall <- struct{}{}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, []string{
			"/actors/abc/123/method/foo",
			"/actors/abc/456/method/foo",
			"/actors/abc/123/method/foo",
		}, g.called.Slice())
	}, time.Second*10, time.Millisecond*10)

	g.holdCall <- struct{}{}

	for range 3 {
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(time.Second * 5):
			assert.Fail(t, "timeout")
		}
	}
}
