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

package daprddisstimeout

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
	suite.Register(new(defaultnoreset))
}

// defaultnoreset verifies that with the default daprd dissemination timeout
// (30s) and a placement-side round that takes ~10s to recover, an actor
// invocation issued during the held window succeeds. Previously, daprd
// hard-coded 5s and would reset the stream + HaltAll mid-invocation,
// causing the call to fail.
type defaultnoreset struct {
	actors *dactors.Actors
	place  *placement.Placement
}

func (d *defaultnoreset) Setup(t *testing.T) []framework.Option {
	d.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second*10),
	)

	d.actors = dactors.New(t,
		dactors.WithActorTypes("myactor"),
		dactors.WithPlacement(d.place),
		dactors.WithActorTypeHandler("myactor", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`OK`))
		}),
	)

	return []framework.Option{
		framework.WithProcesses(d.actors),
	}
}

func (d *defaultnoreset) Run(t *testing.T, ctx context.Context) {
	d.actors.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := d.place.PlacementTables(t, ctx)
		if !assert.NotNil(c, table.Tables["default"]) {
			return
		}
		assert.Len(c, table.Tables["default"].Hosts, 1)
	}, time.Second*15, time.Millisecond*100)

	// Connect a host that never ACKs further messages, holding the
	// dissemination round open. Placement-side will fire its 10s timeout
	// and kick the blocker; daprd's default 30s timeout should NOT fire
	// in the meantime, so an actor invocation issued during the round
	// succeeds rather than being cancelled by a stream reset + HaltAll.
	pclient := d.place.Client(t, ctx)
	blocker, err := pclient.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, blocker.Send(&v1pb.Host{
		Name: "blocker", Port: 9999,
		Entities: []string{"myactor"}, Id: "blocker", Namespace: "default",
	}))
	go func() {
		for {
			if _, recvErr := blocker.Recv(); recvErr != nil {
				return
			}
		}
	}()

	// Wait for placement-side to time out and recover.
	time.Sleep(time.Second * 12)

	// Actor invocation should still succeed: the round completed via the
	// placement-side recovery, the placement table converged, and our
	// daprd never had to halt its actors. With the previous 5s daprd-side
	// timeout, daprd would have HaltAll'd by now and the invocation would
	// hit a transient error.
	httpClient := fclient.HTTP(t)
	reqURL := fmt.Sprintf("http://localhost:%d/v1.0/actors/myactor/test1/method/foo", d.actors.Daprd().HTTPPort())
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, reqURL, nil)
	require.NoError(t, err)
	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
