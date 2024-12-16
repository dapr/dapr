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
	app    *actors.Actors
	called atomic.Int64
}

func (g *grpc) Setup(t *testing.T) []framework.Option {
	g.app = actors.New(t,
		actors.WithActorTypes("abc", "efg"),
		actors.WithActorTypeHandler("abc", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			defer g.called.Add(1)
			if r.Method == nethttp.MethodDelete {
				assert.Equal(t, "/actors/abc/foo", r.URL.Path)
				return
			}
			assert.Equal(t, nethttp.MethodPut, r.Method)
			assert.Equal(t, "/actors/abc/foo/method/timer/foo", r.URL.Path)
			b, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			assert.Equal(t, `{"data":"aGVsbG8=","callback":"","dueTime":"0s","period":"10s"}`, string(b))
		}),
	)

	return []framework.Option{
		framework.WithProcesses(g.app),
	}
}

func (g *grpc) Run(t *testing.T, ctx context.Context) {
	g.app.WaitUntilRunning(t, ctx)

	client := g.app.Daprd().GRPCClient(t, ctx)

	_, err := client.RegisterActorTimer(ctx, &rtv1.RegisterActorTimerRequest{
		ActorType: "abc",
		ActorId:   "foo",
		Name:      "foo",
		DueTime:   "0s",
		Period:    "10s",
		Data:      []byte("hello"),
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, g.called.Load(), int64(1))
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
