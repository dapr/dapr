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
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/proxy"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(partition))
}

// partition asserts a sidecar partitioned from the scheduler halts its
// actors once the scheduler hands them to another host.
type partition struct {
	proxy  *proxy.Proxy
	daprds [2]*daprd.Daprd

	lock      sync.Mutex
	servedOn  map[string]int
	deletedOn map[string]map[int]struct{}
}

func (p *partition) record(path string, host int, deleted bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) < 3 {
		return
	}
	id := parts[2]
	p.lock.Lock()
	defer p.lock.Unlock()
	if deleted {
		if _, ok := p.deletedOn[id]; !ok {
			p.deletedOn[id] = make(map[int]struct{})
		}
		p.deletedOn[id][host] = struct{}{}
		return
	}
	p.servedOn[id] = host
}

func (p *partition) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	p.servedOn = make(map[string]int)
	p.deletedOn = make(map[string]map[int]struct{})

	sched := scheduler.New(t, scheduler.WithPlacementEnabled(true))
	p.proxy = proxy.New(t, sched)

	srvs := make([]*prochttp.HTTP, 2)
	for i := range srvs {
		handler := http.NewServeMux()
		handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, req *http.Request) {
			w.Write([]byte(`{"entities": ["myactortype"]}`))
		})
		handler.HandleFunc("/healthz", func(w http.ResponseWriter, req *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		handler.HandleFunc("/actors/myactortype/", func(w http.ResponseWriter, req *http.Request) {
			p.record(req.URL.Path, i, req.Method == http.MethodDelete)
		})
		srvs[i] = prochttp.New(t, prochttp.WithHandler(handler))
	}

	// Only host 0 goes through the proxy.
	p.daprds[0] = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(srvs[0].Port()),
		daprd.WithSchedulerAddresses(p.proxy.Address()),
	)
	p.daprds[1] = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(srvs[1].Port()),
		daprd.WithScheduler(sched),
	)

	return []framework.Option{
		framework.WithProcesses(sched, p.proxy, srvs[0], srvs[1], p.daprds[0], p.daprds[1]),
	}
}

func (p *partition) Run(t *testing.T, ctx context.Context) {
	p.daprds[0].WaitUntilRunning(t, ctx)
	p.daprds[1].WaitUntilRunning(t, ctx)

	client := p.daprds[1].GRPCClient(t, ctx)
	invoke := func(c *assert.CollectT, actorID string) bool {
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   actorID,
			Method:    "foo",
		})
		return assert.NoError(c, err)
	}
	servedOn := func(actorID string) (int, bool) {
		p.lock.Lock()
		defer p.lock.Unlock()
		host, ok := p.servedOn[actorID]
		return host, ok
	}

	ids := make([]string, 30)
	for i := range ids {
		ids[i] = "actor-" + strconv.Itoa(i)
	}
	var onPartitioned []string
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		onPartitioned = onPartitioned[:0]
		for _, id := range ids {
			if !invoke(c, id) {
				return
			}
			if host, _ := servedOn(id); host == 0 {
				onPartitioned = append(onPartitioned, id)
			}
		}
		assert.NotEmpty(c, onPartitioned)
		assert.Less(c, len(onPartitioned), len(ids))
	}, time.Second*20, time.Millisecond*100)

	p.proxy.Partition(t)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		for _, id := range onPartitioned {
			if !invoke(c, id) {
				return
			}
			host, _ := servedOn(id)
			assert.Equalf(c, 1, host, "actor %q not yet served by the healthy host", id)
		}
	}, time.Second*20, time.Millisecond*100)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		p.lock.Lock()
		defer p.lock.Unlock()
		for _, id := range onPartitioned {
			_, halted := p.deletedOn[id][0]
			assert.Truef(c, halted,
				"actor %q is active on both hosts: the partitioned host never halted it", id)
		}
	}, time.Second*20, time.Millisecond*100)
}
