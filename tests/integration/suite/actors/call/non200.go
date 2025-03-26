/*
Copyright 2025 The Dapr Authors
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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(non200))
}

type non200 struct {
	actors1 *actors.Actors
	actors2 *actors.Actors
	called  atomic.Int64
}

func (n *non200) Setup(t *testing.T) []framework.Option {
	n.actors1 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
			n.called.Add(1)
			w.WriteHeader(nethttp.StatusInternalServerError)
			w.Write([]byte("custom error"))
		}),
	)
	n.actors2 = actors.New(t,
		actors.WithPeerActor(n.actors1),
	)

	return []framework.Option{
		framework.WithProcesses(n.actors1, n.actors2),
	}
}

func (n *non200) Run(t *testing.T, ctx context.Context) {
	n.actors1.WaitUntilRunning(t, ctx)
	n.actors2.WaitUntilRunning(t, ctx)

	for i, actor := range []*actors.Actors{n.actors1, n.actors2} {
		_, err := actor.GRPCClient(t, ctx).InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   "xyz",
			Method:    "foo",
		})
		require.Error(t, err)
		status, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, "error invoke actor method: error from actor service: (500) custom error", status.Message())
		assert.Equal(t, codes.Internal, status.Code())
		assert.Equal(t, int64(i+1), n.called.Load())
	}

	time.Sleep(time.Second * 3)
	assert.Equal(t, int64(2), n.called.Load())

	for i, url := range []string{
		fmt.Sprintf("http://%s/v1.0/actors/abc/xyz/method/foo", n.actors1.Daprd().HTTPAddress()),
		fmt.Sprintf("http://%s/v1.0/actors/abc/xyz/method/foo", n.actors2.Daprd().HTTPAddress()),
	} {
		req, err := nethttp.NewRequest(nethttp.MethodGet, url, nil)
		require.NoError(t, err)
		resp, err := client.HTTP(t).Do(req)
		require.NoError(t, err)
		assert.Equal(t, 500, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		if i == 0 {
			assert.JSONEq(t, `{"errorCode":"ERR_ACTOR_INVOKE_METHOD","message":"error invoke actor method: error from actor service: (500) custom error"}`, string(body))
		} else {
			assert.JSONEq(t, `{"errorCode":"ERR_ACTOR_INVOKE_METHOD","message":"error invoke actor method: rpc error: code = Internal desc = error invoke actor method: error from actor service: (500) custom error"}`, string(body))
		}
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, int64(i+3), n.called.Load())
	}

	time.Sleep(time.Second * 3)
	assert.Equal(t, int64(4), n.called.Load())
}
