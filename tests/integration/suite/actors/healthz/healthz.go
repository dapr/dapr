/*
Copyright 2023 The Dapr Authors
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

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(healthz))
}

// healthz ensures that the daprd `/healthz` endpoint is called and actors will
// respond successfully when invoked.
type healthz struct {
	daprd         *daprd.Daprd
	place         *placement.Placement
	healthzCalled chan struct{}
	once          sync.Once
}

func (h *healthz) Setup(t *testing.T) []framework.Option {
	h.healthzCalled = make(chan struct{})

	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		h.once.Do(func() {
			close(h.healthzCalled)
		})
	})
	handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`OK`))
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	h.place = placement.New(t)
	h.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(h.place.Address()),
		daprd.WithAppProtocol("http"),
		daprd.WithAppPort(srv.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(h.place, srv, h.daprd),
	}
}

func (h *healthz) Run(t *testing.T, ctx context.Context) {
	h.place.WaitUntilRunning(t, ctx)
	h.daprd.WaitUntilTCPReady(t, ctx)

	select {
	case <-h.healthzCalled:
	case <-time.After(time.Second * 15):
		t.Fatal("timed out waiting for healthz call")
	}

	client := util.HTTPClient(t)

	daprdURL := "http://localhost:" + strconv.Itoa(h.daprd.HTTPPort()) + "/v1.0/actors/myactortype/myactorid/method/foo"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, daprdURL, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close())
}
