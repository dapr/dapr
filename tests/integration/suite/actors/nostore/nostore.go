/*
Copyright 2026 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package nostore

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(nostore))
}

// nostore ensures that an app declaring actor entities without an actor state
// store configured does not host actors: its actor types are not advertised
// to placement, so invocation fails to resolve a host.
type nostore struct {
	daprd *daprd.Daprd
}

func (n *nostore) Setup(t *testing.T) []framework.Option {
	handler := chi.NewRouter()
	handler.Get("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.Put("/actors/{actorType}/{actorId}/method/foo", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`"bar"`))
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	place := placement.New(t)

	n.daprd = daprd.New(t,
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithAppProtocol("http"),
		daprd.WithAppPort(srv.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(srv, place, n.daprd),
	}
}

func (n *nostore) Run(t *testing.T, ctx context.Context) {
	n.daprd.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	// The actor type is never advertised so invocation consistently fails to
	// resolve a host, even after giving dissemination time to settle.
	url := fmt.Sprintf("http://%s/v1.0/actors/myactortype/myactor1/method/foo", n.daprd.HTTPAddress())
	for range 5 {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, resp.Body.Close())
		require.NoError(t, err)
		assert.NotEqual(t, http.StatusOK, resp.StatusCode)
		assert.Contains(t, strings.ToLower(string(body)), "did not find address for actor")
		time.Sleep(time.Millisecond * 200)
	}
}
