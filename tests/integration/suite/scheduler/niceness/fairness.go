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

package niceness

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/watchjobs"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(fairness))
}

// fairness verifies that names of equal niceness share a saturated gate in
// turn, rather than the name which queued first draining before the other
// runs.
type fairness struct {
	scheduler *scheduler.Scheduler
}

func (f *fairness) Setup(t *testing.T) []framework.Option {
	f.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(f.scheduler),
	}
}

func (f *fairness) Run(t *testing.T, ctx context.Context) {
	f.scheduler.WaitUntilRunning(t, ctx)
	client := f.scheduler.Client(t, ctx)

	w := watchjobs.Watch(t, ctx, client, &schedulerv1.WatchJobsRequestInitial{
		AppId:             "myapp",
		ActorTypes:        []string{"mytype"},
		ConcurrencyLimits: []*schedulerv1.ConcurrencyLimit{watchjobs.LimitActor("mytype", nil, 1)},
	})

	watchjobs.ScheduleActor(t, ctx, f.scheduler, client, "hold", "myapp", "mytype", "", nil)
	hold := w.Recv(t, "hold")

	for i := range 5 {
		watchjobs.ScheduleActor(t, ctx, f.scheduler, client, "a-"+strconv.Itoa(i), "myapp", "mytype", "a", nil)
	}
	watchjobs.WaitPending(t, ctx, f.scheduler, "mytype", 5)
	for i := range 5 {
		watchjobs.ScheduleActor(t, ctx, f.scheduler, client, "b-"+strconv.Itoa(i), "myapp", "mytype", "b", nil)
	}
	watchjobs.WaitPending(t, ctx, f.scheduler, "mytype", 10)

	w.Ack(t, hold)

	got := make([]string, 0, 10)
	for range 10 {
		resp := w.Recv(t, "pending trigger")
		got = append(got, strings.SplitN(resp.GetName(), "-", 2)[0])
		w.Ack(t, resp)
	}
	assert.Equal(t, []string{"a", "b", "a", "b", "a", "b", "a", "b", "a", "b"}, got)
}
