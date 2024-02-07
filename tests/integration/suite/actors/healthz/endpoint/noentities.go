/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implien.
See the License for the specific language governing permissions and
limitations under the License.
*/

package endpoint

import (
	"context"
	"net/http"
	"sync"
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
	suite.Register(new(noentities))
}

// noentities ensures that the daprd `/healthz` endpoint is called and actors
// will respond successfully when invoked, even when the app healthz endpoint
// is enabled but no entities have been defined.
type noentities struct {
	daprd             *daprd.Daprd
	daprdWithEntities *daprd.Daprd
	place             *placement.Placement
	healthzCalled     chan struct{}
}

func (n *noentities) Setup(t *testing.T) []framework.Option {
	n.healthzCalled = make(chan struct{})

	var once sync.Once
	srvNoEntities := prochttp.New(t,
		prochttp.WithHandlerFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{}`))
		}),
		prochttp.WithHandlerFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			once.Do(func() {
				close(n.healthzCalled)
			})
		}),
	)
	srvWithEntities := prochttp.New(t,
		prochttp.WithHandlerFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"entities": ["myactortype"]}`))
		}),
		prochttp.WithHandlerFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
		prochttp.WithHandlerFunc(pathMethodFoo, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)

	n.place = placement.New(t)
	n.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(n.place.Address()),
		daprd.WithAppProtocol("http"),
		daprd.WithAppPort(srvNoEntities.Port()),
		daprd.WithAppHealthCheck(true),
	)
	n.daprdWithEntities = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(n.place.Address()),
		daprd.WithAppProtocol("http"),
		daprd.WithAppPort(srvWithEntities.Port()),
		daprd.WithAppHealthCheck(true),
	)

	return []framework.Option{
		framework.WithProcesses(n.place, srvNoEntities, srvWithEntities, n.daprd, n.daprdWithEntities),
	}
}

func (n *noentities) Run(t *testing.T, ctx context.Context) {
	n.place.WaitUntilRunning(t, ctx)
	n.daprd.WaitUntilRunning(t, ctx)
	n.daprdWithEntities.WaitUntilRunning(t, ctx)

	for _, tv := range []struct {
		cl           rtv1.DaprClient
		activeActors []*rtv1.ActiveActorsCount
	}{
		{cl: n.daprdWithEntities.GRPCClient(t, ctx), activeActors: []*rtv1.ActiveActorsCount{{Type: "myactortype"}}},
		{cl: n.daprd.GRPCClient(t, ctx), activeActors: nil},
	} {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			meta, err := tv.cl.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			//nolint:testifylint
			assert.NoError(c, err)
			assert.True(c, meta.GetActorRuntime().GetHostReady())
			assert.Equal(c, tv.activeActors, meta.GetActorRuntime().GetActiveActors())
			assert.Equal(c, rtv1.ActorRuntime_RUNNING, meta.GetActorRuntime().GetRuntimeStatus())
			assert.Equal(c, "placement: connected", meta.GetActorRuntime().GetPlacement())
		}, time.Second*30, time.Millisecond*100)
	}

	select {
	case <-n.healthzCalled:
	case <-time.After(time.Second * 15):
		t.Fatal("timed out waiting for healthz call")
	}

	client := util.HTTPClient(t)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fooActorURL(n.daprd), nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close())
}
