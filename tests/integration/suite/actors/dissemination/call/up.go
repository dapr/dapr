/*
Copyright 2026 The Dapr Authors
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
	nethttp "net/http"
	"strconv"
	"strings"
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
	suite.Register(new(up))
}

type up struct {
	app1 *actors.Actors
	app2 *actors.Actors

	holdInDown chan struct{}
	inDelete   slice.Slice[string]
}

func (u *up) Setup(t *testing.T) []framework.Option {
	u.holdInDown = make(chan struct{})
	u.inDelete = slice.String()

	u.app1 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc",
			func(_ nethttp.ResponseWriter, r *nethttp.Request) {
				if r.Method == nethttp.MethodDelete {
					u.inDelete.Append(strings.Split(r.URL.Path, "/")[3])
					<-u.holdInDown
				}
			}),
	)

	u.app2 = actors.New(t,
		actors.WithPeerActor(u.app1),
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc",
			func(_ nethttp.ResponseWriter, r *nethttp.Request) {},
		),
	)

	return []framework.Option{
		framework.WithProcesses(u.app1),
	}
}

func (u *up) Run(t *testing.T, ctx context.Context) {
	u.app1.WaitUntilRunning(t, ctx)

	client := u.app1.GRPCClient(t, ctx)

	const n = 100
	for i := range n {
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   strconv.Itoa(i),
			Method:    "foo",
			Data:      []byte("bar"),
		})
		require.NoError(t, err)
	}

	u.app2.Run(t, ctx)
	t.Cleanup(func() {
		u.app2.Cleanup(t)
	})

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Positive(c, u.inDelete.Len())
	}, time.Second*10, 10*time.Millisecond)

	client = u.app2.GRPCClient(t, ctx)

	ctx, cancel := context.WithTimeout(ctx, time.Second*2)
	t.Cleanup(cancel)
	_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: "abc",
		ActorId:   u.inDelete.Slice()[0],
		Method:    "foo",
	})
	require.Error(t, err)

	close(u.holdInDown)
	u.app2.Cleanup(t)
}
