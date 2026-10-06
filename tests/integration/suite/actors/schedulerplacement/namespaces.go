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
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(namespaces))
}

// namespaces tests that placement tables are scoped by namespace: the same
// actor type & ID activates in each namespace independently &
// invocations never cross namespaces.
type namespaces struct {
	daprdNS1 *daprd.Daprd
	daprdNS2 *daprd.Daprd
	sched    *scheduler.Scheduler

	invokedNS1 atomic.Int64
	invokedNS2 atomic.Int64
}

func (n *namespaces) Setup(t *testing.T) []framework.Option {
	newApp := func(counter *atomic.Int64) *prochttp.HTTP {
		handler := http.NewServeMux()
		handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"entities": ["myactortype"]}`))
		})
		handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		handler.HandleFunc("/actors/myactortype/", func(w http.ResponseWriter, r *http.Request) {
			counter.Add(1)
		})
		return prochttp.New(t, prochttp.WithHandler(handler))
	}

	srv1 := newApp(&n.invokedNS1)
	srv2 := newApp(&n.invokedNS2)

	n.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))

	n.daprdNS1 = daprd.New(t,
		daprd.WithNamespace("ns1"),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(srv1.Port()),
		daprd.WithScheduler(n.sched),
	)
	n.daprdNS2 = daprd.New(t,
		daprd.WithNamespace("ns2"),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(srv2.Port()),
		daprd.WithScheduler(n.sched),
	)

	return []framework.Option{
		framework.WithProcesses(n.sched, srv1, srv2, n.daprdNS1, n.daprdNS2),
	}
}

func (n *namespaces) Run(t *testing.T, ctx context.Context) {
	n.sched.WaitUntilRunning(t, ctx)
	n.daprdNS1.WaitUntilRunning(t, ctx)
	n.daprdNS2.WaitUntilRunning(t, ctx)

	client1 := n.daprdNS1.GRPCClient(t, ctx)
	client2 := n.daprdNS2.GRPCClient(t, ctx)

	invoke := func(t *testing.T, client rtv1.DaprClient, id string) {
		t.Helper()
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
				ActorType: "myactortype", ActorId: id, Method: "foo",
			})
			assert.NoError(c, err)
		}, time.Second*20, time.Millisecond*10)
	}

	// The same actor IDs are driven through both namespaces: every
	// invocation lands on the caller's own app, no call crosses over.
	const numIDs = 30
	for i := range numIDs {
		invoke(t, client1, "actor-"+strconv.Itoa(i))
	}
	assert.Equal(t, int64(numIDs), n.invokedNS1.Load())
	assert.Equal(t, int64(0), n.invokedNS2.Load(),
		"an ns1 actor call reached the ns2 host: namespaces are not isolated")

	for i := range numIDs {
		invoke(t, client2, "actor-"+strconv.Itoa(i))
	}
	assert.Equal(t, int64(numIDs), n.invokedNS2.Load())
	assert.Equal(t, int64(numIDs), n.invokedNS1.Load(),
		"an ns2 actor call reached the ns1 host: namespaces are not isolated")
}
