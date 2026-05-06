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
	"sync/atomic"
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
	suite.Register(new(crossreplica))
}

// crossreplica tests that cross-replica actor invocations work after two
// dissemination timeout+reconnect cycles. An actor invocation from replica A
// that routes to replica B requires both replicas to have functional placement
// clients and correct routing tables.
type crossreplica struct {
	actors []*dactors.Actors
	place  *placement.Placement

	called1 atomic.Int64
	called2 atomic.Int64
}

func (cr *crossreplica) Setup(t *testing.T) []framework.Option {
	cr.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second*2),
	)

	actor1 := dactors.New(t,
		dactors.WithActorTypes("myactor"),
		dactors.WithPlacement(cr.place),
		dactors.WithActorTypeHandler("myactor", func(w http.ResponseWriter, r *http.Request) {
			cr.called1.Add(1)
			w.Write([]byte(`OK`))
		}),
	)
	actor2 := dactors.New(t,
		dactors.WithActorTypes("myactor"),
		dactors.WithPeerActor(actor1),
		dactors.WithActorTypeHandler("myactor", func(w http.ResponseWriter, r *http.Request) {
			cr.called2.Add(1)
			w.Write([]byte(`OK`))
		}),
	)

	cr.actors = []*dactors.Actors{actor1, actor2}

	return []framework.Option{
		framework.WithProcesses(actor1, actor2),
	}
}

func (cr *crossreplica) Run(t *testing.T, ctx context.Context) {
	cr.actors[0].WaitUntilRunning(t, ctx)
	cr.actors[1].WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := cr.place.PlacementTables(t, ctx)
		if !assert.NotNil(c, table.Tables["default"]) {
			return
		}
		assert.Len(c, table.Tables["default"].Hosts, 2)
	}, time.Second*15, time.Millisecond*100)

	httpClient := fclient.HTTP(t)

	var id atomic.Int64
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		reqURL := fmt.Sprintf("http://localhost:%d/v1.0/actors/myactor/%d/method/foo",
			cr.actors[0].Daprd().HTTPPort(), id.Add(1))
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
		assert.NoError(c, err)
		resp, err := httpClient.Do(req)
		assert.NoError(c, err)
		if resp != nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		assert.Positive(c, cr.called1.Load())
		assert.Positive(c, cr.called2.Load())
	}, time.Second*10, time.Millisecond*10)

	connectBlocker := func(t *testing.T, bid string) {
		t.Helper()
		client := cr.place.Client(t, ctx)
		blocker, err := client.ReportDaprStatus(ctx)
		require.NoError(t, err)
		require.NoError(t, blocker.Send(&v1pb.Host{
			Name: bid, Port: 9999,
			Entities: []string{"myactor"}, Id: bid, Namespace: "default",
		}))
		go func() {
			for {
				if _, err := blocker.Recv(); err != nil {
					return
				}
			}
		}()
	}

	for i := range 2 {
		connectBlocker(t, fmt.Sprintf("blocker-%d", i))
		time.Sleep(4 * time.Second)
	}

	cr.called1.Store(0)
	cr.called2.Store(0)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		reqURL := fmt.Sprintf("http://localhost:%d/v1.0/actors/myactor/%d/method/foo",
			cr.actors[0].Daprd().HTTPPort(), id.Add(1))
		req, err := http.NewRequestWithContext(rctx, http.MethodPost, reqURL, nil)
		assert.NoError(c, err)
		resp, err := httpClient.Do(req)
		if !assert.NoError(c, err, "cross-replica actor invocation should not hang after timeout recovery") {
			return
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		assert.Positive(c, cr.called1.Load())
		assert.Positive(c, cr.called2.Load())
	}, time.Second*10, time.Millisecond*100)
}
