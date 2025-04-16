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
	app1     *actors.Actors
	app2     *actors.Actors
	called   atomic.Int64
	holdCall chan struct{}
}

func (g *grpc) Setup(t *testing.T) []framework.Option {
	g.holdCall = make(chan struct{})

	g.app1 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			if r.Method == nethttp.MethodDelete {
				return
			}
			if g.called.Add(1) == 1 {
				return
			}
			<-g.holdCall
		}),
	)

	g.app2 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithPeerActor(g.app1),
		actors.WithActorTypeHandler("abc", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
		}),
	)

	return []framework.Option{
		framework.WithProcesses(g.app1, g.app2),
	}
}

func (g *grpc) Run(t *testing.T, ctx context.Context) {
	g.app1.WaitUntilRunning(t, ctx)
	g.app2.WaitUntilRunning(t, ctx)

	client := g.app2.GRPCClient(t, ctx)

	var i atomic.Int64
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
		assert.Equal(c, int64(2), g.called.Load())
	}, time.Second*10, time.Millisecond*10)

	go func() {
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   strconv.Itoa(int(i.Load())),
			Method:    "bar",
		})
		errCh <- err
	}()

	time.Sleep(time.Second)
	assert.Equal(t, int64(2), g.called.Load())
	g.holdCall <- struct{}{}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(3), g.called.Load())
	}, time.Second*10, time.Millisecond*10)
	close(g.holdCall)

	for range 2 {
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(time.Second * 10):
			assert.Fail(t, "timeout")
		}
	}
}
