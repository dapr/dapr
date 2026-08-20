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
	"maps"
	"net/http"
	"strconv"
	"strings"
	"sync"
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
	suite.Register(new(rebalance))
}

// rebalance asserts a membership change under scheduler placement drains
// only the actors which move host. An actor whose owner is unchanged stays
// active through the dissemination and never sees a deactivation, while a
// moved actor is deactivated on its old host.
type rebalance struct {
	daprds [3]*daprd.Daprd

	lock sync.Mutex
	// servedOn is the host which served the last method call per actor ID.
	servedOn map[string]int
	// deletedOn is the set of hosts which deactivated each actor ID.
	deletedOn map[string]map[int]struct{}
}

func (r *rebalance) record(path string, host int, deleted bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) < 3 {
		return
	}
	id := parts[2]
	r.lock.Lock()
	defer r.lock.Unlock()
	if deleted {
		if _, ok := r.deletedOn[id]; !ok {
			r.deletedOn[id] = make(map[int]struct{})
		}
		r.deletedOn[id][host] = struct{}{}
		return
	}
	r.servedOn[id] = host
}

func (r *rebalance) Setup(t *testing.T) []framework.Option {
	r.servedOn = make(map[string]int)
	r.deletedOn = make(map[string]map[int]struct{})

	sched := scheduler.New(t, scheduler.WithPlacementEnabled(true))

	srvs := make([]*prochttp.HTTP, 3)
	for i := range srvs {
		handler := http.NewServeMux()
		handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, req *http.Request) {
			w.Write([]byte(`{"entities": ["myactortype"]}`))
		})
		handler.HandleFunc("/healthz", func(w http.ResponseWriter, req *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		handler.HandleFunc("/actors/myactortype/", func(w http.ResponseWriter, req *http.Request) {
			r.record(req.URL.Path, i, req.Method == http.MethodDelete)
		})
		srvs[i] = prochttp.New(t, prochttp.WithHandler(handler))
	}

	for i := range r.daprds {
		r.daprds[i] = daprd.New(t,
			daprd.WithInMemoryActorStateStore("mystore"),
			daprd.WithAppPort(srvs[i].Port()),
			daprd.WithScheduler(sched),
		)
	}

	// The third daprd joins mid-test to trigger the membership change.
	return []framework.Option{
		framework.WithProcesses(sched, srvs[0], srvs[1], srvs[2],
			r.daprds[0], r.daprds[1]),
	}
}

func (r *rebalance) Run(t *testing.T, ctx context.Context) {
	r.daprds[0].WaitUntilRunning(t, ctx)
	r.daprds[1].WaitUntilRunning(t, ctx)

	client := r.daprds[0].GRPCClient(t, ctx)
	invoke := func(t *testing.T, actorID string) {
		t.Helper()
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
				ActorType: "myactortype",
				ActorId:   actorID,
				Method:    "foo",
			})
			assert.NoError(c, err)
		}, time.Second*20, time.Millisecond*10)
	}

	ids := make([]string, 50)
	for i := range ids {
		ids[i] = "actor-" + strconv.Itoa(i)
		invoke(t, ids[i])
	}

	r.lock.Lock()
	before := make(map[string]int, len(ids))
	maps.Copy(before, r.servedOn)
	r.lock.Unlock()

	// The third host joins. Fresh probe IDs spreading over all three hosts
	// proves the new membership has disseminated.
	r.daprds[2].Run(t, ctx)
	t.Cleanup(func() { r.daprds[2].Cleanup(t) })
	r.daprds[2].WaitUntilRunning(t, ctx)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		hosts := make(map[int]struct{})
		for i := range 30 {
			id := "probe-" + strconv.Itoa(i)
			invoke(t, id)
			r.lock.Lock()
			hosts[r.servedOn[id]] = struct{}{}
			r.lock.Unlock()
		}
		assert.Len(c, hosts, 3)
	}, time.Second*20, time.Millisecond*100)

	for _, id := range ids {
		invoke(t, id)
	}
	r.lock.Lock()
	after := make(map[string]int, len(ids))
	maps.Copy(after, r.servedOn)
	deleted := make(map[string]map[int]struct{}, len(r.deletedOn))
	for id, hosts := range r.deletedOn {
		cp := make(map[int]struct{}, len(hosts))
		for host := range hosts {
			cp[host] = struct{}{}
		}
		deleted[id] = cp
	}
	r.lock.Unlock()

	moved, stayed := 0, 0
	for _, id := range ids {
		beforeHost, ok := before[id]
		require.Truef(t, ok, "actor %q was never served before the membership change", id)
		afterHost, ok := after[id]
		require.Truef(t, ok, "actor %q was never served after the membership change", id)

		if afterHost == beforeHost {
			stayed++
			assert.Emptyf(t, deleted[id],
				"actor %q kept its host but was deactivated during the membership change", id)
			continue
		}
		moved++
		_, drained := deleted[id][beforeHost]
		assert.Truef(t, drained,
			"actor %q moved host %d to %d without a deactivation on the old host",
			id, beforeHost, afterHost)
		// Rendezvous hashing only reassigns ownership the joining host wins:
		// an actor may move to the new host, never between existing hosts.
		assert.Equalf(t, 2, afterHost,
			"actor %q moved host %d to %d, not to the joining host", id, beforeHost, afterHost)
	}

	// Both populations must exist or the assertions above prove nothing.
	assert.Positive(t, stayed, "no actor kept its host across the membership change")
	assert.Positive(t, moved, "no actor moved host across the membership change")
}
