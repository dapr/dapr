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

package call

import (
	"context"
	"fmt"
	"io"
	nethttp "net/http"
	"sync"
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
	suite.Register(new(concurrent))
}

// concurrent is the integration-level regression test for the actor_features
// e2e flake "Actor concurrency different actor ids". Two different actor IDs
// of the same actor type are invoked concurrently while a placement
// dissemination round is in progress. With per-actor-type lock granularity
// AND a healthy daprd-side dissemination timeout, the unchanged actor type's
// invocations must run in parallel rather than serializing behind a single
// global lock.
type concurrent struct {
	actors *dactors.Actors
	place  *placement.Placement

	inCall      atomic.Int32
	releaseCall chan struct{}

	startedAt sync.Map // map[string]time.Time
}

func (c *concurrent) Setup(t *testing.T) []framework.Option {
	c.releaseCall = make(chan struct{})

	c.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second*10),
	)

	handler := func(w nethttp.ResponseWriter, r *nethttp.Request) {
		c.startedAt.Store(r.URL.Path, time.Now())
		c.inCall.Add(1)
		defer c.inCall.Add(-1)
		select {
		case <-c.releaseCall:
		case <-r.Context().Done():
		}
		w.Write([]byte(`OK`))
	}

	c.actors = dactors.New(t,
		dactors.WithActorTypes("myactor"),
		dactors.WithPlacement(c.place),
		dactors.WithActorTypeHandler("myactor", handler),
	)

	return []framework.Option{
		framework.WithProcesses(c.actors),
	}
}

func (c *concurrent) Run(t *testing.T, ctx context.Context) {
	c.actors.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c2 *assert.CollectT) {
		table := c.place.PlacementTables(t, ctx)
		if !assert.NotNil(c2, table.Tables["default"]) {
			return
		}
		assert.Len(c2, table.Tables["default"].Hosts, 1)
	}, time.Second*15, time.Millisecond*100)

	httpClient := fclient.HTTP(t)
	daprdURL := fmt.Sprintf("http://localhost:%d", c.actors.Daprd().HTTPPort())

	// Hold a placement dissemination round open by connecting a host that
	// never ACKs. Placement-side recovery (its --disseminate-timeout) will
	// resolve this after ~10s; we assert that during the held window, two
	// concurrent invocations to different actor IDs run in parallel.
	pclient := c.place.Client(t, ctx)
	blocker, err := pclient.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, blocker.Send(&v1pb.Host{
		Name: "blocker", Port: 9999,
		Entities: []string{"otheractor"}, // different type to keep "myactor" ring stable
		Id:       "blocker", Namespace: "default",
	}))
	go func() {
		for {
			if _, recvErr := blocker.Recv(); recvErr != nil {
				return
			}
		}
	}()

	// Give LOCK time to propagate to daprd.
	time.Sleep(500 * time.Millisecond)

	// Concurrently invoke two different actor IDs of the same type.
	type result struct {
		statusCode int
		err        error
	}
	results := make(chan result, 2)

	invoke := func(actorID string) {
		url := daprdURL + "/v1.0/actors/myactor/" + actorID + "/method/foo"
		rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		req, rerr := nethttp.NewRequestWithContext(rctx, nethttp.MethodPost, url, nil)
		if rerr != nil {
			results <- result{err: rerr}
			return
		}
		resp, rerr := httpClient.Do(req)
		if rerr != nil {
			results <- result{err: rerr}
			return
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		results <- result{statusCode: resp.StatusCode}
	}

	go invoke("a")
	go invoke("b")

	// Both calls must reach the actor handler in parallel. With the old
	// global-lock behavior, the second call serialized behind the first,
	// so inCall would tick up to 1 then drop to 0 then up to 1 again,
	// never reaching 2 concurrently.
	require.Eventually(t, func() bool {
		return c.inCall.Load() == 2
	}, time.Second*20, time.Millisecond*50,
		"both actor invocations should be in flight concurrently; got %d",
		c.inCall.Load(),
	)

	// Release the calls and verify both completed cleanly.
	close(c.releaseCall)
	for range 2 {
		select {
		case r := <-results:
			require.NoError(t, r.err)
			assert.Equal(t, nethttp.StatusOK, r.statusCode)
		case <-time.After(time.Second * 10):
			require.Fail(t, "timed out waiting for actor invocation")
		}
	}
}
