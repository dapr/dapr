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

package call

import (
	"context"
	"fmt"
	nethttp "net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(self))
}

type self struct {
	app *actors.Actors
	abc atomic.Int64
	efg atomic.Int64
}

func (s *self) Setup(t *testing.T) []framework.Option {
	s.abc.Store(0)
	s.efg.Store(0)

	s.app = actors.New(t,
		actors.WithActorTypes("abc", "efg"),
		actors.WithActorTypeHandler("abc", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			s.abc.Add(1)
		}),
		actors.WithActorTypeHandler("efg", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			s.efg.Add(1)
		}),
	)

	return []framework.Option{
		framework.WithProcesses(s.app),
	}
}

func (s *self) Run(t *testing.T, ctx context.Context) {
	s.app.WaitUntilRunning(t, ctx)

	gclient := s.app.Daprd().GRPCClient(t, ctx)
	_, err := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: "abc",
		ActorId:   "a123",
		Method:    "foo",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), s.abc.Load())

	_, err = gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: "efg",
		ActorId:   "a123",
		Method:    "foo",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), s.efg.Load())

	hclient := client.HTTP(t)
	url := fmt.Sprintf("http://%s/v1.0/actors/abc/a123/method/foo", s.app.Daprd().HTTPAddress())
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
	require.NoError(t, err)
	resp, err := hclient.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, int64(2), s.abc.Load())

	url = fmt.Sprintf("http://%s/v1.0/actors/efg/a123/method/foo", s.app.Daprd().HTTPAddress())
	req, err = nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
	require.NoError(t, err)
	resp, err = hclient.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, int64(2), s.efg.Load())
}
