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
	"strings"
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
	suite.Register(new(state))
}

// state tests that actor state persisted before a dissemination timeout is
// still accessible after two timeout+reconnect cycles. This exercises the
// HaltAll → actor deactivation → state persistence → re-activation path.
type state struct {
	actors *dactors.Actors
	place  *placement.Placement
}

func (s *state) Setup(t *testing.T) []framework.Option {
	s.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second*7),
	)

	s.actors = dactors.New(t,
		dactors.WithActorTypes("myactor"),
		dactors.WithPlacement(s.place),
		dactors.WithActorTypeHandler("myactor", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`OK`))
		}),
	)

	return []framework.Option{
		framework.WithProcesses(s.actors),
	}
}

func (s *state) Run(t *testing.T, ctx context.Context) {
	s.actors.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := s.place.PlacementTables(t, ctx)
		if !assert.NotNil(c, table.Tables["default"]) {
			return
		}
		assert.Len(c, table.Tables["default"].Hosts, 1)
	}, time.Second*15, time.Millisecond*100)

	httpClient := fclient.HTTP(t)
	daprdURL := fmt.Sprintf("http://localhost:%d", s.actors.Daprd().HTTPPort())

	reqURL := daprdURL + "/v1.0/actors/myactor/stateful1/method/foo"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	require.NoError(t, err)
	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	stateURL := daprdURL + "/v1.0/actors/myactor/stateful1/state"
	stateBody := `[{"operation":"upsert","request":{"key":"color","value":"blue"}}]`
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, stateURL, strings.NewReader(stateBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err = httpClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp.Body.Close()

	connectBlocker := func(t *testing.T, id string) {
		t.Helper()
		client := s.place.Client(t, ctx)
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

	req, err = http.NewRequestWithContext(rctx, http.MethodPost, reqURL, nil)
	require.NoError(t, err)
	resp, err = httpClient.Do(req)
	require.NoError(t, err, "actor re-activation should not hang after timeout recovery")
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	getURL := daprdURL + "/v1.0/actors/myactor/stateful1/state/color"
	req, err = http.NewRequestWithContext(rctx, http.MethodGet, getURL, nil)
	require.NoError(t, err)
	resp, err = httpClient.Do(req)
	require.NoError(t, err)
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, `"blue"`, string(b))
}
