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
	"fmt"
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
	"github.com/dapr/kit/concurrency/slice"
)

func init() {
	suite.Register(new(grpc))
}

type grpc struct {
	app1         *actors.Actors
	app2         *actors.Actors
	called       atomic.Int64
	methodCalled slice.Slice[string]
	holdCall     chan struct{}
}

func (g *grpc) Setup(t *testing.T) []framework.Option {
	g.holdCall = make(chan struct{})
	g.methodCalled = slice.String()
	g.called.Store(0)

	g.app1 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			if r.Method == nethttp.MethodDelete {
				return
			}
			if g.called.Add(1) < 3 {
				return
			}
			g.methodCalled.Append(r.URL.Path)
			<-g.holdCall
		}),
	)

	g.app2 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithPeerActor(g.app1),
		actors.WithActorTypeHandler("abc", func(nethttp.ResponseWriter, *nethttp.Request) {}),
	)

	return []framework.Option{
		framework.WithProcesses(g.app1, g.app2),
	}
}

func (g *grpc) Run(t *testing.T, ctx context.Context) {
	g.app1.WaitUntilRunning(t, ctx)
	g.app2.WaitUntilRunning(t, ctx)

	client := g.app2.GRPCClient(t, ctx)

	var i, j atomic.Int64
	for {
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   strconv.Itoa(int(i.Add(1))),
			Method:    "foo",
		})
		require.NoError(t, err)
		if g.called.Load() == 1 {
			break
		}
	}
	j.Store(i.Load())
	for {
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   strconv.Itoa(int(j.Add(1))),
			Method:    "foo",
		})
		require.NoError(t, err)
		if g.called.Load() == 2 {
			break
		}
	}

	errCh := make(chan error)
	go func() {
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   strconv.Itoa(int(i.Load())),
			Method:    "foo",
		})
		errCh <- err
	}()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, []string{
			fmt.Sprintf("/actors/abc/%d/method/foo", i.Load()),
		}, g.methodCalled.Slice())
	}, time.Second*10, time.Millisecond*10)

	go func() {
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   strconv.Itoa(int(j.Load())),
			Method:    "bar",
		})
		errCh <- err
	}()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, []string{
			fmt.Sprintf("/actors/abc/%d/method/foo", i.Load()),
			fmt.Sprintf("/actors/abc/%d/method/bar", j.Load()),
		}, g.methodCalled.Slice())
	}, time.Second*10, time.Millisecond*10)

	go func() {
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   strconv.Itoa(int(i.Load())),
			Method:    "foo",
		})
		errCh <- err
	}()

	time.Sleep(time.Second)
	assert.Equal(t, []string{
		fmt.Sprintf("/actors/abc/%d/method/foo", i.Load()),
		fmt.Sprintf("/actors/abc/%d/method/bar", j.Load()),
	}, g.methodCalled.Slice())

	g.holdCall <- struct{}{}
	g.holdCall <- struct{}{}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, []string{
			fmt.Sprintf("/actors/abc/%d/method/foo", i.Load()),
			fmt.Sprintf("/actors/abc/%d/method/bar", j.Load()),
			fmt.Sprintf("/actors/abc/%d/method/foo", i.Load()),
		}, g.methodCalled.Slice())
	}, time.Second*10, time.Millisecond*10)

	close(g.holdCall)

	for range 3 {
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(time.Second * 5):
			assert.Fail(t, "timeout")
		}
	}
}
