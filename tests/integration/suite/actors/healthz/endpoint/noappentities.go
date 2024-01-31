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
	suite.Register(new(noappentities))
}

// noappentities ensures that actors work even though no entities have been
// defined and the app healthchecks is disabled.
type noappentities struct {
	daprdNoHealthz  *daprd.Daprd
	daprd           *daprd.Daprd
	place           *placement.Placement
	healthzCalled   chan struct{}
	noHealthzCalled chan struct{}
}

func (n *noappentities) Setup(t *testing.T) []framework.Option {
	n.healthzCalled = make(chan struct{})
	n.noHealthzCalled = make(chan struct{})
	n.place = placement.New(t)

	var onceHealthz, onceNoHealthz sync.Once

	srvHealthz := prochttp.New(t,
		prochttp.WithHandlerFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"entities": ["myactortype"]}`))
		}),
		prochttp.WithHandlerFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			onceHealthz.Do(func() {
				close(n.healthzCalled)
			})
		}),
		prochttp.WithHandlerFunc(pathMethodFoo, func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`OK`))
		}),
	)

	srvNoHealthz := prochttp.New(t,
		prochttp.WithHandlerFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{}`))
		}),
		prochttp.WithHandlerFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			onceNoHealthz.Do(func() {
				close(n.noHealthzCalled)
			})
		}),
	)

	n.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(n.place.Address()),
		daprd.WithAppProtocol("http"),
		daprd.WithAppPort(srvHealthz.Port()),
		daprd.WithAppHealthCheck(true),
	)
	n.daprdNoHealthz = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(n.place.Address()),
		daprd.WithAppProtocol("http"),
		daprd.WithAppPort(srvNoHealthz.Port()),
		daprd.WithAppHealthCheck(false),
	)

	return []framework.Option{
		framework.WithProcesses(n.place, srvHealthz, srvNoHealthz, n.daprd, n.daprdNoHealthz),
	}
}

func (n *noappentities) Run(t *testing.T, ctx context.Context) {
	n.place.WaitUntilRunning(t, ctx)
	n.daprd.WaitUntilAppHealth(t, ctx)
	n.daprdNoHealthz.WaitUntilAppHealth(t, ctx)

	for _, tv := range []struct {
		cl           rtv1.DaprClient
		activeActors []*rtv1.ActiveActorsCount
	}{
		{cl: n.daprd.GRPCClient(t, ctx), activeActors: []*rtv1.ActiveActorsCount{{Type: "myactortype"}}},
		{cl: n.daprdNoHealthz.GRPCClient(t, ctx), activeActors: nil},
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
		assert.Fail(t, "timed out waiting for healthz call")
	}

	client := util.HTTPClient(t)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fooActorURL(n.daprd), nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	select {
	case <-n.noHealthzCalled:
		assert.Fail(t, "healthz on health disabled and empty entities should not be called")
	default:
	}

	meta, err := n.daprd.GRPCClient(t, ctx).GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)
	assert.True(t, meta.GetActorRuntime().GetHostReady())
	assert.Len(t, meta.GetActorRuntime().GetActiveActors(), 1)
	assert.Equal(t, rtv1.ActorRuntime_RUNNING, meta.GetActorRuntime().GetRuntimeStatus())
	assert.Equal(t, "placement: connected", meta.GetActorRuntime().GetPlacement())
}
