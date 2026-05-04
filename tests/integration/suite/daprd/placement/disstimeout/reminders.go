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
	suite.Register(new(reminders))
}

// reminders tests that actor reminders registered before a dissemination
// timeout continue to fire after two timeout+reconnect cycles. During the
// timeout window, the scheduler still holds the reminder but the inflight lock
// is nil, so reminder delivery is blocked. After recovery, reminders must
// resume.
type reminders struct {
	actors *dactors.Actors
	place  *placement.Placement

	reminderCalled atomic.Int64
}

func (rm *reminders) Setup(t *testing.T) []framework.Option {
	rm.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second*2),
	)

	rm.actors = dactors.New(t,
		dactors.WithActorTypes("myactor"),
		dactors.WithPlacement(rm.place),
		dactors.WithActorTypeHandler("myactor", func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/method/remind/") {
				rm.reminderCalled.Add(1)
			}
			w.Write([]byte(`OK`))
		}),
	)

	return []framework.Option{
		framework.WithProcesses(rm.actors),
	}
}

func (rm *reminders) Run(t *testing.T, ctx context.Context) {
	rm.actors.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := rm.place.PlacementTables(t, ctx)
		if !assert.NotNil(c, table.Tables["default"]) {
			return
		}
		assert.Len(c, table.Tables["default"].Hosts, 1)
	}, time.Second*15, time.Millisecond*100)

	httpClient := fclient.HTTP(t)
	daprdURL := fmt.Sprintf("http://localhost:%d", rm.actors.Daprd().HTTPPort())

	reqURL := daprdURL + "/v1.0/actors/myactor/reminder1/method/foo"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	require.NoError(t, err)
	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	connectBlocker := func(t *testing.T, id string) {
		t.Helper()
		client := rm.place.Client(t, ctx)
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
		time.Sleep(4 * time.Second)
	}

	rm.reminderCalled.Store(0)
	reminderURL := daprdURL + "/v1.0/actors/myactor/reminder1/reminders/myreminder"
	reminderBody := `{"dueTime":"0ms","period":"1s"}`
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err = http.NewRequestWithContext(rctx, http.MethodPost, reminderURL, strings.NewReader(reminderBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err = httpClient.Do(req)
	require.NoError(t, err, "reminder registration should not hang after timeout recovery")
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Positive(c, rm.reminderCalled.Load())
	}, time.Second*5, time.Millisecond*100)
}
