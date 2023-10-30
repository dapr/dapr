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
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/exp/slices"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(deactivateOnUnhealthy))
}

// deactivateOnUnhealthy ensures that all active actors are deactivated if the app is reported as unhealthy.
type deactivateOnUnhealthy struct {
	daprd               *daprd.Daprd
	place               *placement.Placement
	isHealthy           atomic.Bool
	invokedActorsCh     chan string
	deactivatedActorsCh chan string
}

func (h *deactivateOnUnhealthy) Setup(t *testing.T) []framework.Option {
	h.isHealthy.Store(true)
	h.invokedActorsCh = make(chan string, 2)
	h.deactivatedActorsCh = make(chan string, 2)

	r := chi.NewRouter()
	r.Get("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if h.isHealthy.Load() {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`OK`))
	})
	r.Delete("/actors/{actorType}/{actorId}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		act := fmt.Sprintf("%s/%s", chi.URLParam(r, "actorType"), chi.URLParam(r, "actorId"))
		log.Printf("Received deactivation for actor %s", act)
		h.deactivatedActorsCh <- act
	})
	r.Put("/actors/{actorType}/{actorId}/method/foo", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`bar`))
		act := fmt.Sprintf("%s/%s", chi.URLParam(r, "actorType"), chi.URLParam(r, "actorId"))
		log.Printf("Received invocation for actor %s", act)
		h.invokedActorsCh <- act
	})

	srv := prochttp.New(t, prochttp.WithHandler(r))
	h.place = placement.New(t)
	h.daprd = daprd.New(t, daprd.WithResourceFiles(`
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
		daprd.WithPlacementAddresses("localhost:"+strconv.Itoa(h.place.Port())),
		daprd.WithAppProtocol("http"),
		daprd.WithAppPort(srv.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(h.place, srv, h.daprd),
	}
}

func (h *deactivateOnUnhealthy) Run(t *testing.T, ctx context.Context) {
	h.place.WaitUntilRunning(t, ctx)
	h.daprd.WaitUntilRunning(t, ctx)

	client := util.HTTPClient(t)

	// Sleep a bit at the beginning as it takes time for the actor host to be registered
	time.Sleep(500 * time.Millisecond)

	// Activate 2 actors
	for i := 0; i < 2; i++ {
		assert.EventuallyWithT(t, func(t *assert.CollectT) {
			daprdURL := fmt.Sprintf("http://localhost:%d/v1.0/actors/myactortype/myactor%d/method/foo", h.daprd.HTTPPort(), i)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, daprdURL, nil)
			assert.NoError(t, err)
			resp, err := client.Do(req)
			assert.NoError(t, err)
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			assert.NoError(t, err)
			assert.Equalf(t, http.StatusOK, resp.StatusCode, "Response body: %v", string(body))
		}, 10*time.Second, 100*time.Millisecond, "actor not ready")
	}

	// Validate invocations
	invoked := make([]string, 2)
	for i := 0; i < 2; i++ {
		invoked[i] = <-h.invokedActorsCh
	}
	slices.Sort(invoked)
	assert.Equal(t, []string{"myactortype/myactor0", "myactortype/myactor1"}, invoked)

	// Terminate the placement process to simulate a failure
	h.place.Cleanup(t)

	// Ensure actors get deactivated
	deactivated := make([]string, 0, 2)
	require.Eventually(t, func() bool {
		select {
		case act := <-h.deactivatedActorsCh:
			deactivated = append(deactivated, act)
		case <-time.After(50 * time.Millisecond):
			return false
		}

		return len(deactivated) == 2
	}, 5*time.Second, 50*time.Millisecond, "Did not receive 2 deactivation signals")
	slices.Sort(deactivated)
	assert.Equal(t, []string{"myactortype/myactor0", "myactortype/myactor1"}, deactivated)
}
