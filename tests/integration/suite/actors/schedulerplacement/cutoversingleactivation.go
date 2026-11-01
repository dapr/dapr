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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(cutoversingleactivation))
}

// cutoversingleactivation cuts three actor hosts over from the placement
// service to the scheduler. The two authorities hash differently, so the
// cutover relocates actors, and every relocated actor must deactivate on its
// old host before activating on the new one, with no overlapping active
// windows and no unbounded invocation outage.
type cutoversingleactivation struct {
	sched  *scheduler.Scheduler
	place  *placement.Placement
	daprds [3]*daprd.Daprd

	lock       sync.Mutex
	firstServe map[string]map[int]time.Time
	deactivate map[string]map[int]time.Time
}

func (a *cutoversingleactivation) record(path string, host int, deleted bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) < 3 {
		return
	}
	id := parts[2]
	now := time.Now()
	a.lock.Lock()
	defer a.lock.Unlock()
	if deleted {
		if _, ok := a.deactivate[id]; !ok {
			a.deactivate[id] = make(map[int]time.Time)
		}
		if _, ok := a.deactivate[id][host]; !ok {
			a.deactivate[id][host] = now
		}
		return
	}
	if _, ok := a.firstServe[id]; !ok {
		a.firstServe[id] = make(map[int]time.Time)
	}
	if _, ok := a.firstServe[id][host]; !ok {
		a.firstServe[id][host] = now
	}
}

func (a *cutoversingleactivation) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	a.firstServe = make(map[string]map[int]time.Time)
	a.deactivate = make(map[string]map[int]time.Time)

	a.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))
	a.place = placement.New(t)

	procs := make([]process.Interface, 0, 2+2*len(a.daprds))
	procs = append(procs, a.sched, a.place)
	for i := range a.daprds {
		handler := http.NewServeMux()
		handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, req *http.Request) {
			w.Write([]byte(`{"entities": ["myactortype"]}`))
		})
		handler.HandleFunc("/healthz", func(w http.ResponseWriter, req *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		handler.HandleFunc("/actors/myactortype/", func(w http.ResponseWriter, req *http.Request) {
			a.record(req.URL.Path, i, req.Method == http.MethodDelete)
		})
		srv := prochttp.New(t, prochttp.WithHandler(handler))
		a.daprds[i] = daprd.New(t,
			daprd.WithInMemoryActorStateStore("mystore"),
			daprd.WithAppPort(srv.Port()),
			daprd.WithScheduler(a.sched),
			daprd.WithPlacementAddresses(a.place.Address()),
		)
		procs = append(procs, srv, a.daprds[i])
	}

	return []framework.Option{framework.WithProcesses(procs...)}
}

func (a *cutoversingleactivation) Run(t *testing.T, ctx context.Context) {
	a.sched.WaitUntilRunning(t, ctx)
	a.place.WaitUntilRunning(t, ctx)
	for _, d := range a.daprds {
		d.WaitUntilRunning(t, ctx)
	}

	const instances = 30
	ids := make([]string, instances)
	for i := range ids {
		ids[i] = fmt.Sprintf("cutover-%d", i)
	}

	client := a.daprds[0].GRPCClient(t, ctx)
	invoke := func(id string) error {
		ictx, cancel := context.WithTimeout(ctx, time.Second*2)
		defer cancel()
		_, err := client.InvokeActor(ictx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   id,
			Method:    "foo",
		})
		return err
	}

	for _, id := range ids {
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.NoError(c, invoke(id))
		}, time.Second*20, time.Millisecond*10)
	}

	a.lock.Lock()
	before := make(map[string]int, instances)
	for id, hosts := range a.firstServe {
		for host := range hosts {
			before[id] = host
		}
	}
	a.lock.Unlock()
	require.Len(t, before, instances)

	// The continuity prober measures the longest invocation outage across
	// the cutover.
	proberCtx, proberCancel := context.WithCancel(ctx)
	proberDone := make(chan struct{})
	var maxOutage time.Duration
	go func() {
		defer close(proberDone)
		var outageStart time.Time
		i := 0
		for {
			select {
			case <-proberCtx.Done():
				return
			case <-time.After(time.Millisecond * 50):
			}
			err := invoke(ids[i%len(ids)])
			i++
			if err != nil {
				if outageStart.IsZero() {
					outageStart = time.Now()
				}
				continue
			}
			if !outageStart.IsZero() {
				if d := time.Since(outageStart); d > maxOutage {
					maxOutage = d
				}
				outageStart = time.Time{}
			}
		}
	}()

	a.place.Cleanup(t)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var streams float64
		for k, v := range a.sched.Metrics(c, ctx).All() {
			if strings.HasPrefix(k, "dapr_scheduler_placement_streams_connected") {
				streams += v
			}
		}
		assert.GreaterOrEqual(c, streams, float64(3))
	}, time.Second*15, time.Millisecond*10)

	for _, id := range ids {
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.NoError(c, invoke(id))
		}, time.Second*20, time.Millisecond*10)
	}

	proberCancel()
	<-proberDone
	assert.Less(t, maxOutage, time.Second*20,
		"the cutover must not black-hole invocations beyond the reconnect budget")

	a.lock.Lock()
	defer a.lock.Unlock()
	moved, stayed := 0, 0
	for _, id := range ids {
		beforeHost := before[id]
		serves := a.firstServe[id]
		require.NotEmpty(t, serves)
		if len(serves) == 1 {
			stayed++
			continue
		}
		moved++
		for host, served := range serves {
			if host == beforeHost {
				continue
			}
			deactivated, ok := a.deactivate[id][beforeHost]
			assert.Truef(t, ok,
				"actor %q activated on host %d while never deactivated on host %d", id, host, beforeHost)
			if ok {
				assert.Falsef(t, served.Before(deactivated),
					"actor %q was active on hosts %d and %d at once", id, beforeHost, host)
			}
		}
	}
	assert.Positive(t, moved, "no actor moved across the cutover, the rehash was not exercised")
	assert.Positive(t, stayed, "every actor moved across the cutover")
}
