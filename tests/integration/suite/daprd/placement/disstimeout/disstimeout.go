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
	suite.Register(new(disstimeout))
}

type disstimeout struct {
	actors *dactors.Actors
	place  *placement.Placement
}

func (d *disstimeout) Setup(t *testing.T) []framework.Option {
	d.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second*7),
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

func (d *disstimeout) Run(t *testing.T, ctx context.Context) {
	d.actors.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := d.place.PlacementTables(t, ctx)
		if !assert.NotNil(c, table.Tables["default"]) {
			return
		}
		assert.Len(c, table.Tables["default"].Hosts, 1)
	}, time.Second*15, time.Millisecond*100)

	httpClient := fclient.HTTP(t)
	reqURL := fmt.Sprintf("http://localhost:%d/v1.0/actors/myactor/test1/method/foo", d.actors.Daprd().HTTPPort())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	require.NoError(t, err)
	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	connectBlocker := func(t *testing.T, id string) {
		t.Helper()
		client := d.place.Client(t, ctx)
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

		rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		req, err = http.NewRequestWithContext(rctx, http.MethodPost, reqURL, nil)
		require.NoError(t, err)
		resp, err = httpClient.Do(req)
		cancel()
		require.NoError(t, err, "actor invocation should not hang after dissemination timeout cycle %d", i)
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	}
}
