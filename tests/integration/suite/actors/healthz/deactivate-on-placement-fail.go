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
	"testing"
	"time"

	chi "github.com/go-chi/chi/v5"
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
	suite.Register(new(deactivateOnPlacementFail))
}

// deactivateOnPlacementFail ensures that all active actors are deactivated if the connection with Placement fails.
type deactivateOnPlacementFail struct {
	daprd               *daprd.Daprd
	place               *placement.Placement
	invokedActorsCh     chan string
	deactivatedActorsCh chan string
}

func (h *deactivateOnPlacementFail) Setup(t *testing.T) []framework.Option {
	h.invokedActorsCh = make(chan string, 2)
	h.deactivatedActorsCh = make(chan string, 2)

	r := chi.NewRouter()
	r.Get("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
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
		daprd.WithPlacementAddresses("127.0.0.1:"+strconv.Itoa(h.place.Port())),
		daprd.WithAppProtocol("http"),
		daprd.WithAppPort(srv.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(h.place, srv, h.daprd),
	}
}

func (h *deactivateOnPlacementFail) Run(t *testing.T, ctx context.Context) {
	h.place.WaitUntilRunning(t, ctx)
	h.daprd.WaitUntilRunning(t, ctx)

	client := util.HTTPClient(t)

	// Activate 2 actors
	for i := 0; i < 2; i++ {
		assert.EventuallyWithT(t, func(t *assert.CollectT) {
			daprdURL := fmt.Sprintf("http://localhost:%d/v1.0/actors/myactortype/myactor%d/method/foo", h.daprd.HTTPPort(), i)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, daprdURL, nil)
			require.NoError(t, err)
			resp, err := client.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			assert.Equalf(t, http.StatusOK, resp.StatusCode, "Response body: %v", string(body))
		}, 10*time.Second, 100*time.Millisecond, "actor not ready")
	}

	// Validate invocations
	invoked := make([]string, 2)
	for i := 0; i < 2; i++ {
		select {
		case invoked[i] = <-h.invokedActorsCh:
		case <-time.After(time.Second * 5):
			assert.Fail(t, "failed to invoke actor in time")
		}
	}
	assert.ElementsMatch(t, []string{"myactortype/myactor0", "myactortype/myactor1"}, invoked)

	// Terminate the placement process to simulate a failure
	h.place.Cleanup(t)

	// Ensure actors get deactivated
	deactivated := make([]string, 2)
	for i := range deactivated {
		select {
		case deactivated[i] = <-h.deactivatedActorsCh:
		case <-time.After(5 * time.Second):
			assert.Fail(t, "Did not receive deactivation signal in time")
		}
	}
	assert.ElementsMatch(t, []string{"myactortype/myactor0", "myactortype/myactor1"}, deactivated)
}
