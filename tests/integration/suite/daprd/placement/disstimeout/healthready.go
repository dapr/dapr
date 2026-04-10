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

package disstimeout

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	fclient "github.com/dapr/dapr/tests/integration/framework/client"
	dactors "github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(healthready))
}

// healthready tests that daprd's health endpoint returns healthy after two
// dissemination timeout+reconnect cycles. During the timeout, handleCloseStream
// sets placement ready=false and healthTarget is only set Ready() after UNLOCK.
// After recovery, the daprd must report healthy again.
type healthready struct {
	actors *dactors.Actors
	place  *placement.Placement
}

func (h *healthready) Setup(t *testing.T) []framework.Option {
	h.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second*7),
	)

	h.actors = dactors.New(t,
		dactors.WithActorTypes("myactor"),
		dactors.WithPlacement(h.place),
		dactors.WithActorTypeHandler("myactor", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`OK`))
		}),
	)

	return []framework.Option{
		framework.WithProcesses(h.actors),
	}
}

func (h *healthready) Run(t *testing.T, ctx context.Context) {
	h.actors.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := h.place.PlacementTables(t, ctx)
		if !assert.NotNil(c, table.Tables["default"]) {
			return
		}
		assert.Len(c, table.Tables["default"].Hosts, 1)
	}, time.Second*15, time.Millisecond*100)

	httpClient := fclient.HTTP(t)
	healthURL := fmt.Sprintf("http://localhost:%d/v1.0/healthz", h.actors.Daprd().HTTPPort())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	require.NoError(t, err)
	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	connectBlocker := func(t *testing.T, id string) {
		t.Helper()
		client := h.place.Client(t, ctx)
		blocker, berr := client.ReportDaprStatus(ctx)
		require.NoError(t, berr)
		require.NoError(t, blocker.Send(&v1pb.Host{
			Name: id, Port: 9999,
			Entities: []string{"myactor"}, Id: id, Namespace: "default",
		}))
		go func() {
			for {
				if _, recvErr := blocker.Recv(); recvErr != nil {
					return
				}
			}
		}()
	}

	for i := range 2 {
		connectBlocker(t, fmt.Sprintf("blocker-%d", i))
		time.Sleep(9 * time.Second)
	}

	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err = http.NewRequestWithContext(rctx, http.MethodGet, healthURL, nil)
	require.NoError(t, err)
	resp, err = httpClient.Do(req)
	require.NoError(t, err, "health endpoint should respond after timeout recovery")
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode, "daprd should report healthy after timeout recovery")
}
