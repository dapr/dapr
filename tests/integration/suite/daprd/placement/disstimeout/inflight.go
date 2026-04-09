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
	suite.Register(new(inflight))
}

// inflight tests that actor invocations started DURING the LOCK phase (when
// the inflight lock is nil) are unblocked after timeout recovery, not abandoned
// forever. Callers wait on a response channel with a select on ctx.Done(). If
// HaltAll doesn't cancel their context (e.g. the actor was never created), the
// caller hangs until the caller's own context deadline fires.
type inflight struct {
	actors *dactors.Actors
	place  *placement.Placement
}

func (inf *inflight) Setup(t *testing.T) []framework.Option {
	inf.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second*7),
	)

	inf.actors = dactors.New(t,
		dactors.WithActorTypes("myactor"),
		dactors.WithPlacement(inf.place),
		dactors.WithActorTypeHandler("myactor", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`OK`))
		}),
	)

	return []framework.Option{
		framework.WithProcesses(inf.actors),
	}
}

func (inf *inflight) Run(t *testing.T, ctx context.Context) {
	inf.actors.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := inf.place.PlacementTables(t, ctx)
		if !assert.NotNil(c, table.Tables["default"]) {
			return
		}
		assert.Len(c, table.Tables["default"].Hosts, 1)
	}, time.Second*15, time.Millisecond*100)

	httpClient := fclient.HTTP(t)
	daprdURL := fmt.Sprintf("http://localhost:%d", inf.actors.Daprd().HTTPPort())

	client := inf.place.Client(t, ctx)
	blocker, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, blocker.Send(&v1pb.Host{
		Name: "blocker", Port: 9999,
		Entities: []string{"myactor"}, Id: "blocker", Namespace: "default",
	}))
	go func() {
		for {
			if _, err := blocker.Recv(); err != nil {
				return
			}
		}
	}()

	time.Sleep(time.Second)

	inflightDone := make(chan int, 1)
	go func() {
		rctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		reqURL := daprdURL + "/v1.0/actors/myactor/newactor/method/foo"
		req, err := http.NewRequestWithContext(rctx, http.MethodPost, reqURL, nil)
		if err != nil {
			inflightDone <- 0
			return
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			inflightDone <- 0
			return
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		inflightDone <- resp.StatusCode
	}()

	select {
	case code := <-inflightDone:
		assert.Equal(t, http.StatusOK, code,
			"actor invocation queued during LOCK phase should complete after recovery")
	case <-time.After(12 * time.Second):
		require.Fail(t, "actor invocation queued during LOCK phase hung indefinitely")
	}
}
