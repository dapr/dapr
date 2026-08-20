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
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(hashing))
}

const (
	hashingActorType = "myactortype"
	hashingActorIDs  = 50
)

// hashing runs the same assertions against both algorithms, the placement
// service's hash ring and the scheduler's rendezvous hashing: determinism
// (same owner no matter which sidecar is asked), agreement (all sidecars
// resolve identically), and coverage (ownership spreads across hosts). A
// divergence fails on exactly one topology.
type hashing struct {
	ring       *hashingTopology
	rendezvous *hashingTopology
}

// hashingTopology is one actor cluster of two hosts, placed by one authority.
type hashingTopology struct {
	name   string
	daprds []*daprd.Daprd

	lock sync.Mutex
	// activatedOn maps actor ID to the set of host indexes which activated
	// it. More than one entry for an ID is a single activation violation.
	activatedOn map[string]map[int]struct{}
}

func newHashingTopology(t *testing.T, name string) (*hashingTopology, []*prochttp.HTTP) {
	t.Helper()

	topo := &hashingTopology{
		name:        name,
		activatedOn: make(map[string]map[int]struct{}),
	}

	srvs := make([]*prochttp.HTTP, 3)
	for i := range srvs {
		handler := http.NewServeMux()
		handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"entities": ["` + hashingActorType + `"]}`))
		})
		handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		handler.HandleFunc("/actors/"+hashingActorType+"/", func(w http.ResponseWriter, r *http.Request) {
			topo.record(actorIDFromPath(r.URL.Path), i)
		})
		srvs[i] = prochttp.New(t, prochttp.WithHandler(handler))
	}

	return topo, srvs
}

// actorIDFromPath pulls the actor ID out of /actors/<type>/<id>[/method/...].
func actorIDFromPath(path string) string {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) < 3 {
		return ""
	}
	return parts[2]
}

func (h *hashingTopology) record(actorID string, host int) {
	if actorID == "" {
		return
	}
	h.lock.Lock()
	defer h.lock.Unlock()
	if _, ok := h.activatedOn[actorID]; !ok {
		h.activatedOn[actorID] = make(map[int]struct{})
	}
	h.activatedOn[actorID][host] = struct{}{}
}

// reset starts a new activation epoch, so assertions after a membership
// change are not polluted by activations legitimately made before it.
func (h *hashingTopology) reset() {
	h.lock.Lock()
	defer h.lock.Unlock()
	h.activatedOn = make(map[string]map[int]struct{})
}

func (h *hashingTopology) snapshot() map[string]map[int]struct{} {
	h.lock.Lock()
	defer h.lock.Unlock()
	out := make(map[string]map[int]struct{}, len(h.activatedOn))
	for id, hosts := range h.activatedOn {
		cp := make(map[int]struct{}, len(hosts))
		for host := range hosts {
			cp[host] = struct{}{}
		}
		out[id] = cp
	}
	return out
}

func (h *hashing) Setup(t *testing.T) []framework.Option {
	// Ring: the standalone placement service. The scheduler is present but
	// does not serve placement, which is what a pre-cutover cluster looks
	// like.
	ringTopo, ringSrvs := newHashingTopology(t, "ring")
	ringPlace := placement.New(t)
	ringSched := scheduler.New(t)
	h.ring = ringTopo
	for i := range ringSrvs {
		h.ring.daprds = append(h.ring.daprds, daprd.New(t,
			daprd.WithInMemoryActorStateStore("mystore"),
			daprd.WithAppPort(ringSrvs[i].Port()),
			daprd.WithScheduler(ringSched),
			daprd.WithPlacementAddresses(ringPlace.Address()),
		))
	}

	// Rendezvous: placement served by the scheduler, no placement service.
	rvTopo, rvSrvs := newHashingTopology(t, "rendezvous")
	rvSched := scheduler.New(t, scheduler.WithPlacementEnabled(true))
	h.rendezvous = rvTopo
	for i := range rvSrvs {
		h.rendezvous.daprds = append(h.rendezvous.daprds, daprd.New(t,
			daprd.WithInMemoryActorStateStore("mystore"),
			daprd.WithAppPort(rvSrvs[i].Port()),
			daprd.WithScheduler(rvSched),
		))
	}

	// The third daprd of each topology joins mid-test to exercise a
	// membership change.
	procs := []framework.Option{
		framework.WithProcesses(
			ringPlace, ringSched, rvSched,
			ringSrvs[0], ringSrvs[1], ringSrvs[2],
			rvSrvs[0], rvSrvs[1], rvSrvs[2],
			h.ring.daprds[0], h.ring.daprds[1],
			h.rendezvous.daprds[0], h.rendezvous.daprds[1],
		),
	}
	return procs
}

func (h *hashing) Run(t *testing.T, ctx context.Context) {
	for _, topo := range []*hashingTopology{h.ring, h.rendezvous} {
		t.Run(topo.name, func(t *testing.T) {
			for _, d := range topo.daprds[:2] {
				d.WaitUntilRunning(t, ctx)
			}
			topo.run(t, ctx)
		})
	}
}

func (h *hashingTopology) run(t *testing.T, ctx context.Context) {
	t.Helper()

	clients := make([]rtv1.DaprClient, 2)
	for i, d := range h.daprds[:2] {
		clients[i] = d.GRPCClient(t, ctx)
	}

	invoke := func(t *testing.T, client rtv1.DaprClient, actorID string) {
		t.Helper()
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
				ActorType: hashingActorType,
				ActorId:   actorID,
				Method:    "foo",
			})
			assert.NoError(c, err)
		}, time.Second*20, time.Millisecond*10)
	}

	ids := make([]string, hashingActorIDs)
	for i := range ids {
		ids[i] = "actor-" + strconv.Itoa(i)
	}

	// Drive every actor ID through the first sidecar, then through the
	// second. Both must resolve the same owner, so the second pass must not
	// activate a single actor anywhere new.
	for _, id := range ids {
		invoke(t, clients[0], id)
	}
	afterFirst := h.snapshot()

	for _, id := range ids {
		invoke(t, clients[1], id)
	}
	afterSecond := h.snapshot()

	t.Run("every actor activates on exactly one host", func(t *testing.T) {
		require.Len(t, afterSecond, len(ids))
		for _, id := range ids {
			hosts, ok := afterSecond[id]
			require.Truef(t, ok, "actor %q never activated", id)
			assert.Lenf(t, hosts, 1,
				"actor %q activated on %d hosts: single activation violated", id, len(hosts))
		}
	})

	t.Run("ownership is identical whichever sidecar is asked", func(t *testing.T) {
		for _, id := range ids {
			assert.Equalf(t, afterFirst[id], afterSecond[id],
				"actor %q moved host when invoked through a different sidecar", id)
		}
	})

	t.Run("ownership is spread across both hosts", func(t *testing.T) {
		owners := make(map[int]int, 2)
		for _, hosts := range afterSecond {
			for host := range hosts {
				owners[host]++
			}
		}
		assert.Lenf(t, owners, 2,
			"only %d of 2 hosts own actors: %v", len(owners), owners)
		for host, count := range owners {
			assert.Positivef(t, count, "host %d owns no actors", host)
		}

		// The runtime's own accounting must agree: each daprd's metadata
		// reports the same active actor count its handler observed.
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			for i, d := range h.daprds[:2] {
				active := 0
				for _, aa := range d.GetMetaActorRuntime(c, ctx).ActiveActors {
					if aa.Type == hashingActorType {
						active = aa.Count
					}
				}
				assert.Equalf(c, owners[i], active,
					"host %d metadata active actor count disagrees with observed activations", i)
			}
		}, time.Second*10, time.Millisecond*50)
	})

	t.Run("repeated lookups are stable", func(t *testing.T) {
		// Ownership must not drift while membership is unchanged. A
		// non-deterministic hash would show up here as an actor appearing on
		// a second host.
		for _, id := range ids[:10] {
			invoke(t, clients[0], id)
			invoke(t, clients[1], id)
		}
		for _, id := range ids[:10] {
			assert.Lenf(t, h.snapshot()[id], 1,
				"actor %q gained a second host on repeated lookup", id)
		}
	})

	t.Run("membership change keeps single activation", func(t *testing.T) {
		before := h.snapshot()

		// A third host of the same actor type joins.
		h.daprds[2].Run(t, ctx)
		t.Cleanup(func() { h.daprds[2].Cleanup(t) })
		h.daprds[2].WaitUntilRunning(t, ctx)

		// Wait for the new membership to be disseminated: fresh actor IDs
		// spread over all three hosts.
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			h.reset()
			for i := range 30 {
				invoke(t, clients[0], "join-probe-"+strconv.Itoa(i))
			}
			owners := make(map[int]struct{})
			for _, hosts := range h.snapshot() {
				for host := range hosts {
					owners[host] = struct{}{}
				}
			}
			assert.Len(c, owners, 3)
		}, time.Second*20, time.Millisecond*100)

		// New epoch: every actor again resolves identically and activates
		// on one host, and unmoved actors stay where they were.
		h.reset()
		for _, id := range ids {
			invoke(t, clients[0], id)
		}
		for _, id := range ids {
			invoke(t, clients[1], id)
		}

		after := h.snapshot()
		moved := 0
		for _, id := range ids {
			hosts, ok := after[id]
			require.Truef(t, ok, "actor %q never activated after membership change", id)
			require.Lenf(t, hosts, 1,
				"actor %q activated on %d hosts after membership change: single activation violated", id, len(hosts))
			for host := range hosts {
				if _, was := before[id][host]; !was {
					moved++
				}
			}
		}
		// Both algorithms move only a minority of actors on a single host
		// join. A full reshuffle would indicate the table was rebuilt with
		// different inputs on different sidecars.
		assert.Lessf(t, moved, len(ids), "every actor moved on membership change")
	})
}
