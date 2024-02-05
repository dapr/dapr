/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or impliea.
See the License for the specific language governing permissions and
limitations under the License.
*/

package endpoint

import (
	"context"
	"net/http"
	"strconv"
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

const (
	pathMethodFoo = "/actors/myactortype/myactorid/method/foo"
)

func init() {
	suite.Register(new(allenabled))
}

// allenabled ensures that the daprd `/healthz` endpoint is called and actors
// will respond successfully when invoked when both app health check and
// entities are enabled.
type allenabled struct {
	daprd         *daprd.Daprd
	place         *placement.Placement
	healthzCalled chan struct{}
	rootCalled    atomic.Bool
}

func (a *allenabled) Setup(t *testing.T) []framework.Option {
	a.healthzCalled = make(chan struct{})

	var once sync.Once
	srv := prochttp.New(t,
		prochttp.WithHandlerFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"entities": ["myactortype"]}`))
		}),
		prochttp.WithHandlerFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			once.Do(func() {
				close(a.healthzCalled)
			})
		}),
		prochttp.WithHandlerFunc(pathMethodFoo, func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`OK`))
		}),
		prochttp.WithHandlerFunc("/", func(w http.ResponseWriter, r *http.Request) {
			a.rootCalled.Store(true)
			w.WriteHeader(http.StatusInternalServerError)
		}),
	)

	a.place = placement.New(t)
	a.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(a.place.Address()),
		daprd.WithAppProtocol("http"),
		daprd.WithAppPort(srv.Port()),
		daprd.WithAppHealthCheck(true),
	)

	return []framework.Option{
		framework.WithProcesses(a.place, srv, a.daprd),
	}
}

func (a *allenabled) Run(t *testing.T, ctx context.Context) {
	a.place.WaitUntilRunning(t, ctx)
	a.daprd.WaitUntilRunning(t, ctx)

	gclient := a.daprd.GRPCClient(t, ctx)

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
	case <-a.healthzCalled:
	case <-time.After(time.Second * 15):
		t.Fatal("timed out waiting for healthz call")
	}

	client := util.HTTPClient(t)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fooActorURL(a.daprd), nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close())
}

func fooActorURL(daprd *daprd.Daprd) string {
	return "http://127.0.0.1:" + strconv.Itoa(daprd.HTTPPort()) + "/v1.0" + pathMethodFoo
}
