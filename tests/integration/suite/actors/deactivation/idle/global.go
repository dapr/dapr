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

package idle

import (
	"context"
	"net/http"
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
	suite.Register(new(global))
}

type global struct {
	app    *actors.Actors
	called slice.Slice[string]
}

func (g *global) Setup(t *testing.T) []framework.Option {
	g.called = slice.String()

	g.app = actors.New(t,
		actors.WithActorTypes("abc", "def", "xyz"),
		actors.WithActorIdleTimeout(2*time.Second),
		actors.WithActorTypeHandler("abc", func(_ http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				g.called.Append(r.URL.Path)
				return
			}
		}),
		actors.WithActorTypeHandler("def", func(_ http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				g.called.Append(r.URL.Path)
				return
			}
		}),
	)

	return []framework.Option{
		framework.WithProcesses(g.app),
	}
}

func (g *global) Run(t *testing.T, ctx context.Context) {
	g.app.WaitUntilRunning(t, ctx)

	_, err := g.app.GRPCClient(t, ctx).InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: "abc",
		ActorId:   "123",
		Method:    "foo",
	})
	require.NoError(t, err)

	time.Sleep(time.Second)
	assert.Equal(t, 0, g.called.Len())

	_, err = g.app.GRPCClient(t, ctx).InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: "abc",
		ActorId:   "123",
		Method:    "foo",
	})
	require.NoError(t, err)

	_, err = g.app.GRPCClient(t, ctx).InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: "abc",
		ActorId:   "456",
		Method:    "foo",
	})
	require.NoError(t, err)

	_, err = g.app.GRPCClient(t, ctx).InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: "def",
		ActorId:   "456",
		Method:    "foo",
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.ElementsMatch(c, []string{
			"/actors/abc/123",
			"/actors/abc/456",
			"/actors/def/456",
		}, g.called.Slice())
	}, time.Second*10, time.Millisecond*10)
}
