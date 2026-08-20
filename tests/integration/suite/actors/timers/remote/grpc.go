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
	"io"
	nethttp "net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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

	timer1, timer2 atomic.Int64
}

func (g *grpc) Setup(t *testing.T) []framework.Option {
	g.app1 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithHandler("/actors/abc/{id}", func(nethttp.ResponseWriter, *nethttp.Request) {}),
		actors.WithHandler("/actors/abc/{id}/method/timer/foo", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			assert.Equal(t, nethttp.MethodPut, r.Method)
			b, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			assert.JSONEq(t, `{"data":"aGVsbG8=","callback":"","dueTime":"0s","period":"1s"}`, string(b))
			g.timer1.Add(1)
		}),
	)

	g.app2 = actors.New(t,
		actors.WithPeerActor(g.app1),
		actors.WithActorTypes("abc"),
		actors.WithHandler("/actors/abc/{id}", func(nethttp.ResponseWriter, *nethttp.Request) {}),
		actors.WithHandler("/actors/abc/{id}/method/timer/foo", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			assert.Equal(t, nethttp.MethodPut, r.Method)
			b, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			assert.JSONEq(t, `{"data":"aGVsbG8=","callback":"","dueTime":"0s","period":"1s"}`, string(b))
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

	client1 := g.app1.Daprd().GRPCClient(t, ctx)
	client2 := g.app2.Daprd().GRPCClient(t, ctx)

	register := func(client rtv1.DaprClient, id string) error {
		_, err := client.RegisterActorTimer(ctx, &rtv1.RegisterActorTimerRequest{
			ActorType: "abc",
			ActorId:   id,
			Name:      "foo",
			DueTime:   "0s",
			Period:    "1s",
			Data:      []byte("hello"),
		})
		return err
	}

	owner1, owner2 := "", ""
	for i := 0; owner1 == "" || owner2 == ""; i++ {
		require.Less(t, i, 100, "actor IDs never hashed to both hosts")
		id := strconv.Itoa(i)
		err1 := register(client1, id)
		err2 := register(client2, id)
		require.False(t, err1 == nil && err2 == nil, "both hosts accepted the same timer registration")
		require.False(t, err1 != nil && err2 != nil, "both hosts rejected the timer registration")
		if err1 == nil {
			require.Equal(t, grpccodes.PermissionDenied, status.Code(err2))
			if owner1 == "" {
				owner1 = id
			}
		} else {
			require.Equal(t, grpccodes.PermissionDenied, status.Code(err1))
			if owner2 == "" {
				owner2 = id
			}
		}
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Positive(c, g.timer1.Load())
		assert.Positive(c, g.timer2.Load())
	}, time.Second*10, time.Millisecond*10)

	_, err := client2.UnregisterActorTimer(ctx, &rtv1.UnregisterActorTimerRequest{
		ActorType: "abc", ActorId: owner1, Name: "foo",
	})
	require.Equal(t, grpccodes.PermissionDenied, status.Code(err))
	_, err = client1.UnregisterActorTimer(ctx, &rtv1.UnregisterActorTimerRequest{
		ActorType: "abc", ActorId: owner1, Name: "foo",
	})
	require.NoError(t, err)
	_, err = client2.UnregisterActorTimer(ctx, &rtv1.UnregisterActorTimerRequest{
		ActorType: "abc", ActorId: owner2, Name: "foo",
	})
	require.NoError(t, err)
}
