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

package deactivation

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
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(reconnect))
}

type reconnect struct {
	app *actors.Actors
}

func (r *reconnect) Setup(t *testing.T) []framework.Option {
	r.app = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", func(w http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodDelete {
				w.WriteHeader(http.StatusInternalServerError)
			}
		}),
	)

	return []framework.Option{
		framework.WithProcesses(r.app),
	}
}

func (r *reconnect) Run(t *testing.T, ctx context.Context) {
	r.app.WaitUntilRunning(t, ctx)

	client := r.app.GRPCClient(t, ctx)
	_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: "abc",
		ActorId:   "1",
		Method:    "foo",
	})
	require.NoError(t, err)

	place := r.app.Placement()
	place.Cleanup(t)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		meta := r.app.Daprd().GetMetaActorRuntime(c, ctx)
		if !assert.NotNil(c, meta) {
			return
		}
		assert.Equal(c, "placement: disconnected", meta.Placement)
	}, time.Second*10, time.Millisecond*10)

	newPlace := placement.New(t, placement.WithPort(place.Port()))
	t.Cleanup(func() { newPlace.Cleanup(t) })
	newPlace.Run(t, ctx)
	newPlace.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		meta := r.app.Daprd().GetMetaActorRuntime(c, ctx)
		if !assert.NotNil(c, meta) {
			return
		}
		assert.Equal(c, "placement: connected", meta.Placement)
		assert.True(c, meta.HostReady)
	}, time.Second*30, time.Millisecond*10)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		ictx, cancel := context.WithTimeout(ctx, time.Second*2)
		defer cancel()
		_, ierr := client.InvokeActor(ictx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   "1",
			Method:    "foo",
		})
		assert.NoError(c, ierr)
	}, time.Second*30, time.Millisecond*10)
}
