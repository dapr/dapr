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

package schedulerplacement

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(unreachable))
}

// unreachable gives the sidecar an unreachable scheduler and a healthy
// placement service: the bounded wait for a scheduler placement answer
// expires and the placement service serves actors.
type unreachable struct {
	place *placement.Placement
	daprd *daprd.Daprd

	invoked atomic.Int64
}

func (u *unreachable) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/", func(w http.ResponseWriter, r *http.Request) {
		u.invoked.Add(1)
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))

	// The reserved port is never freed, so the scheduler address stays
	// unreachable for the whole test.
	deadAddr := fmt.Sprintf("127.0.0.1:%d", ports.Reserve(t, 1).Port(t))

	u.place = placement.New(t)
	u.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(srv.Port()),
		daprd.WithSchedulerAddresses(deadAddr),
		daprd.WithPlacementAddresses(u.place.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(u.place, srv, u.daprd),
	}
}

func (u *unreachable) Run(t *testing.T, ctx context.Context) {
	u.place.WaitUntilRunning(t, ctx)

	// The daprd healthz stays gated on its scheduler connection, so wait
	// on the actor APIs directly.
	client := u.daprd.GRPCClient(t, ctx)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Method:    "foo",
		})
		assert.NoError(c, err)
	}, time.Second*60, time.Millisecond*100)
	assert.Positive(t, u.invoked.Load())
}
