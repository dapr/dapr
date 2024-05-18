/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or impliei.
See the License for the specific language governing permissions and
limitations under the License.
*/

package healthz

import (
	"context"
	"net/http"
	"strconv"
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
	suite.Register(new(initerror))
}

// initerror tests that Daprd will block actor calls until actors have been
// initialized.
type initerror struct {
	daprd         *daprd.Daprd
	place         *placement.Placement
	configCalled  chan struct{}
	blockConfig   chan struct{}
	healthzCalled chan struct{}
}

func (i *initerror) Setup(t *testing.T) []framework.Option {
	i.configCalled = make(chan struct{})
	i.blockConfig = make(chan struct{})
	i.healthzCalled = make(chan struct{})

	var once sync.Once

	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		close(i.configCalled)
		<-i.blockConfig
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		once.Do(func() {
			close(i.healthzCalled)
		})
	})
	handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`OK`))
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	i.place = placement.New(t)
	i.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(i.place.Address()),
		daprd.WithAppPort(srv.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(i.place, srv, i.daprd),
	}
}

func (i *initerror) Run(t *testing.T, ctx context.Context) {
	i.place.WaitUntilRunning(t, ctx)
	i.daprd.WaitUntilTCPReady(t, ctx)

	client := util.HTTPClient(t)

	select {
	case <-i.configCalled:
	case <-time.After(time.Second * 5):
		t.Fatal("timed out waiting for config call")
	}

	rctx, cancel := context.WithTimeout(ctx, time.Second*2)
	t.Cleanup(cancel)
	daprdURL := "http://127.0.0.1:" + strconv.Itoa(i.daprd.HTTPPort()) + "/v1.0/actors/myactortype/myactorid/method/foo"

	req, err := http.NewRequestWithContext(rctx, http.MethodPost, daprdURL, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	if resp != nil && resp.Body != nil {
		require.NoError(t, resp.Body.Close())
	}

	meta, err := i.daprd.GRPCClient(t, ctx).GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)
	assert.Empty(t, meta.GetActorRuntime().GetActiveActors())
	assert.Equal(t, rtv1.ActorRuntime_INITIALIZING, meta.GetActorRuntime().GetRuntimeStatus())

	close(i.blockConfig)

	select {
	case <-i.healthzCalled:
	case <-time.After(time.Second * 15):
		assert.Fail(t, "timed out waiting for healthz call")
	}

	req, err = http.NewRequestWithContext(ctx, http.MethodPost, daprdURL, nil)
	require.NoError(t, err)
	resp, err = client.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	meta, err = i.daprd.GRPCClient(t, ctx).GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)
	if assert.Len(t, meta.GetActorRuntime().GetActiveActors(), 1) {
		assert.Equal(t, "myactortype", meta.GetActorRuntime().GetActiveActors()[0].GetType())
	}
	assert.Equal(t, rtv1.ActorRuntime_RUNNING, meta.GetActorRuntime().GetRuntimeStatus())
}
