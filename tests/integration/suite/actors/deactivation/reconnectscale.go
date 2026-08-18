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
	"strconv"
	"testing"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(reconnectscale))
}

type reconnectscale struct {
	place  *placement.Placement
	daprds []*daprd.Daprd
}

const reconnectscaleReplicas = 30

func (r *reconnectscale) Setup(t *testing.T) []framework.Option {
	handler := chi.NewRouter()
	handler.Get("/dapr/config", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.Get("/healthz", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.Delete("/actors/{actorType}/{actorId}", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	handler.Put("/actors/{actorType}/{actorId}/method/foo", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(`bar`))
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	r.place = placement.New(t)

	r.daprds = make([]*daprd.Daprd, reconnectscaleReplicas)
	procs := make([]process.Interface, 0, len(r.daprds)+2)
	procs = append(procs, r.place, srv)
	for i := range r.daprds {
		r.daprds[i] = daprd.New(t, daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: state.in-memory
  version: v1
  metadata:
  - name: actorStateStore
    value: true
`),
			daprd.WithPlacementAddresses("127.0.0.1:"+strconv.Itoa(r.place.Port())),
			daprd.WithAppProtocol("http"),
			daprd.WithAppPort(srv.Port()),
		)
		procs = append(procs, r.daprds[i])
	}

	return []framework.Option{
		framework.WithProcesses(procs...),
	}
}

func (r *reconnectscale) Run(t *testing.T, ctx context.Context) {
	r.place.WaitUntilRunning(t, ctx)
	for _, d := range r.daprds {
		d.WaitUntilRunning(t, ctx)
	}

	invokeAll := func(c *assert.CollectT, client rtv1.DaprClient) {
		for i := range reconnectscaleReplicas * 2 {
			ictx, cancel := context.WithTimeout(ctx, time.Second*5)
			_, err := client.InvokeActor(ictx, &rtv1.InvokeActorRequest{
				ActorType: "myactortype",
				ActorId:   strconv.Itoa(i),
				Method:    "foo",
			})
			cancel()
			if !assert.NoError(c, err) {
				return
			}
		}
	}

	client := r.daprds[0].GRPCClient(t, ctx)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		invokeAll(c, client)
	}, time.Second*30, time.Millisecond*10)

	r.place.Cleanup(t)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		for _, d := range r.daprds {
			meta := d.GetMetaActorRuntime(c, ctx)
			if !assert.NotNil(c, meta) {
				return
			}
			if !assert.Equal(c, "placement: disconnected", meta.Placement) {
				return
			}
		}
	}, time.Second*30, time.Millisecond*10)

	newPlace := placement.New(t, placement.WithPort(r.place.Port()))
	t.Cleanup(func() { newPlace.Cleanup(t) })
	newPlace.Run(t, ctx)
	newPlace.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		for _, d := range r.daprds {
			meta := d.GetMetaActorRuntime(c, ctx)
			if !assert.NotNil(c, meta) {
				return
			}
			if !assert.Equal(c, "placement: connected", meta.Placement) {
				return
			}
			if !assert.True(c, meta.HostReady) {
				return
			}
		}
	}, time.Second*60, time.Millisecond*10)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		invokeAll(c, client)
	}, time.Second*30, time.Millisecond*10)
}
