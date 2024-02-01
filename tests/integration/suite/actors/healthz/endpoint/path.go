/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or impliep.
See the License for the specific language governing permissions and
limitations under the License.
*/

package endpoint

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(path))
}

// path tests that the healthz endpoint is called when the path is changed.
type path struct {
	daprd         *daprd.Daprd
	place         *placement.Placement
	healthzCalled chan struct{}
	customHealthz chan struct{}
	rootCalled    atomic.Bool
}

func (p *path) Setup(t *testing.T) []framework.Option {
	p.healthzCalled = make(chan struct{})
	p.customHealthz = make(chan struct{})

	var honce, conce sync.Once
	srv := prochttp.New(t,
		prochttp.WithHandlerFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"entities": ["myactortype"]}`))
		}),
		prochttp.WithHandlerFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			honce.Do(func() {
				close(p.healthzCalled)
			})
		}),
		prochttp.WithHandlerFunc("/customhealthz", func(w http.ResponseWriter, r *http.Request) {
			conce.Do(func() {
				close(p.customHealthz)
			})
		}),
		prochttp.WithHandlerFunc(pathMethodFoo, func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`OK`))
		}),
		prochttp.WithHandlerFunc("/", func(w http.ResponseWriter, r *http.Request) {
			p.rootCalled.Store(true)
			w.WriteHeader(http.StatusInternalServerError)
		}),
	)
	p.place = placement.New(t)
	p.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(p.place.Address()),
		daprd.WithAppPort(srv.Port()),
		daprd.WithAppHealthCheckPath("/customhealthz"),
		daprd.WithAppHealthCheck(true),
		daprd.WithAppHealthProbeInterval(1),
		daprd.WithAppHealthProbeThreshold(1),
	)

	return []framework.Option{
		framework.WithProcesses(p.place, srv, p.daprd),
	}
}

func (p *path) Run(t *testing.T, ctx context.Context) {
	p.place.WaitUntilRunning(t, ctx)
	p.daprd.WaitUntilRunning(t, ctx)

	gclient := p.daprd.GRPCClient(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, err := gclient.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		//nolint:testifylint
		assert.NoError(c, err)
		assert.True(c, meta.GetActorRuntime().GetHostReady())
		assert.Len(c, meta.GetActorRuntime().GetActiveActors(), 1)
		assert.Equal(c, rtv1.ActorRuntime_RUNNING, meta.GetActorRuntime().GetRuntimeStatus())
		assert.Equal(c, "placement: connected", meta.GetActorRuntime().GetPlacement())
	}, time.Second*30, time.Millisecond*100)

	select {
	case <-p.customHealthz:
	case <-time.After(time.Second * 15):
		assert.Fail(t, "timed out waiting for healthz call")
	}

	select {
	case <-p.healthzCalled:
	case <-time.After(time.Second * 15):
		assert.Fail(t, "/healthz endpoint should still have been called for actor health check")
	}

	client := util.HTTPClient(t)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fooActorURL(p.daprd), nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	assert.False(t, p.rootCalled.Load())
}
