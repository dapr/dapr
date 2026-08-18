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
	suite.Register(new(repeated))
}

// repeated tests that a daprd sidecar survives two consecutive dissemination
// timeout cycles. After the first blocker is resolved and the sidecar
// reconnects, a second blocker triggers the same scenario. The sidecar must
// recover both times.
type repeated struct {
	actors *dactors.Actors
	place  *placement.Placement
}

func (r *repeated) Setup(t *testing.T) []framework.Option {
	r.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second*2),
	)

	r.actors = dactors.New(t,
		dactors.WithActorTypes("myactor"),
		dactors.WithPlacement(r.place),
		dactors.WithActorTypeHandler("myactor", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`OK`))
		}),
	)

	return []framework.Option{
		framework.WithProcesses(r.actors),
	}
}

func (r *repeated) Run(t *testing.T, ctx context.Context) {
	r.actors.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := r.place.PlacementTables(t, ctx)
		if !assert.NotNil(c, table.Tables["default"]) {
			return
		}
		assert.Len(c, table.Tables["default"].Hosts, 1)
	}, time.Second*15, time.Millisecond*100)

	httpClient := fclient.HTTP(t)
	reqURL := fmt.Sprintf("http://localhost:%d/v1.0/actors/myactor/test1/method/foo", r.actors.Daprd().HTTPPort())

	connectBlocker := func(t *testing.T, id string) {
		t.Helper()
		client := r.place.Client(t, ctx)
		blocker, err := client.ReportDaprStatus(ctx)
		require.NoError(t, err)
		require.NoError(t, blocker.Send(&v1pb.Host{
			Name:      id,
			Port:      9999,
			Entities:  []string{"myactor"},
			Id:        id,
			Namespace: "default",
		}))
		go func() {
			for {
				if _, err := blocker.Recv(); err != nil {
					return
				}
			}
		}()
	}

	assertActorWorks := func(t *testing.T) {
		t.Helper()
		rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(rctx, http.MethodPost, reqURL, nil)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err, "actor invocation should not hang")
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	}

	connectBlocker(t, "blocker-1")
	time.Sleep(4 * time.Second)
	assertActorWorks(t)

	connectBlocker(t, "blocker-2")
	time.Sleep(4 * time.Second)
	assertActorWorks(t)
}
