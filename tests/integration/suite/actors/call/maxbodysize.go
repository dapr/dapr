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
	nethttp "net/http"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(maxbodysize))
}

type maxbodysize struct {
	actors1 *actors.Actors
	actors2 *actors.Actors

	called1 atomic.Bool
	called2 atomic.Bool
}

func (m *maxbodysize) Setup(t *testing.T) []framework.Option {
	m.actors1 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithMaxBodySize("1k"),
		actors.WithActorTypeHandler("abc", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			m.called1.Store(true)
		}),
	)
	m.actors2 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithPeerActor(m.actors1),
		actors.WithMaxBodySize("1k"),
		actors.WithActorTypeHandler("abc", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			m.called2.Store(true)
		}),
	)

	return []framework.Option{
		framework.WithProcesses(m.actors1, m.actors2),
	}
}

func (m *maxbodysize) Run(t *testing.T, ctx context.Context) {
	m.actors1.WaitUntilRunning(t, ctx)
	m.actors2.WaitUntilRunning(t, ctx)

	client := m.actors1.GRPCClient(t, ctx)

	var host1 *int
	var host2 *int

	for i := 0; ; i++ {
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   strconv.Itoa(i),
			Method:    "foo",
		})
		require.NoError(t, err)

		if m.called1.Load() && host1 == nil {
			host1 = &i
		}

		if m.called2.Load() && host2 == nil {
			host2 = &i
		}

		if m.called1.Load() && m.called2.Load() {
			break
		}
	}

	for i := range []int{*host1, *host2} {
		smallBody := make([]byte, 500)
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   strconv.Itoa(i),
			Method:    "foo",
			Data:      smallBody,
		})
		require.NoError(t, err)

		largeBody := make([]byte, 2048)
		_, err = client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   strconv.Itoa(i),
			Method:    "foo",
			Data:      largeBody,
		})
		require.Error(t, err)
		status, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.ResourceExhausted, status.Code())
		assert.Equal(t, "grpc: received message larger than max (2064 vs. 1000)", status.Message())
	}
}
