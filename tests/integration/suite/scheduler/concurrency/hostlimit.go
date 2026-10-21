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

package concurrency

import (
	"context"
	"strconv"
	"testing"
	"time"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/watchjobs"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(hostlimit))
}

// hostlimit verifies that the scheduler keeps no more triggers in flight for
// an actor type than the sum of the per-sidecar limits its hosts advertise,
// counting each host once however many streams it opens.
type hostlimit struct {
	scheduler *scheduler.Scheduler
}

func (h *hostlimit) Setup(t *testing.T) []framework.Option {
	h.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(h.scheduler),
	}
}

func (h *hostlimit) Run(t *testing.T, ctx context.Context) {
	h.scheduler.WaitUntilRunning(t, ctx)
	client := h.scheduler.Client(t, ctx)

	initial := func(addr string) *schedulerv1.WatchJobsRequestInitial {
		return &schedulerv1.WatchJobsRequestInitial{
			AppId:                 "myapp",
			ActorTypes:            []string{"mytype"},
			ActorAddress:          ptr.Of(addr),
			HostConcurrencyLimits: []*schedulerv1.ConcurrencyLimit{watchjobs.LimitActor("mytype", nil, 5)},
		}
	}
	watchers := []*watchjobs.Watcher{
		watchjobs.Watch(t, ctx, client, initial("10.0.0.1:50002")),
		watchjobs.Watch(t, ctx, client, initial("10.0.0.1:50002")),
		watchjobs.Watch(t, ctx, client, initial("10.0.0.2:50002")),
	}

	for i := range 30 {
		watchjobs.ScheduleActor(t, ctx, h.scheduler, client, "job-"+strconv.Itoa(i), "myapp", "mytype", "", nil)
	}
	watchjobs.WaitPending(t, ctx, h.scheduler, "mytype", 20)

	received := 0
	deadline := time.After(3 * time.Second)
	for done := false; !done; {
		select {
		case <-watchers[0].RecvCh:
			received++
		case <-watchers[1].RecvCh:
			received++
		case <-watchers[2].RecvCh:
			received++
		case <-deadline:
			done = true
		}
	}
	if received != 10 {
		t.Fatalf("expected 10 triggers in flight for two hosts advertising 5 each, got %d", received)
	}
}
