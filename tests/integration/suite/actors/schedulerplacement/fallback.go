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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(fallback))
}

// fallback tests that a daprd with the SchedulerPlacement feature enabled
// falls back to the standalone placement service when the scheduler cluster
// does not serve placement (old scheduler), keeping actors working.
type fallback struct {
	daprd *daprd.Daprd
	sched *scheduler.Scheduler
	place *placement.Placement
	log   *logline.LogLine

	invoked atomic.Int64
}

func (f *fallback) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/", func(w http.ResponseWriter, r *http.Request) {
		f.invoked.Add(1)
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	// Scheduler does NOT serve placement.
	f.sched = scheduler.New(t)
	f.place = placement.New(t)
	f.log = logline.New(t, logline.WithStdoutLineContains(
		"scheduler cluster does not serve placement; falling back to the placement service",
	))
	f.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(srv.Port()),
		daprd.WithScheduler(f.sched),
		daprd.WithPlacementAddresses(f.place.Address()),
		daprd.WithConfigManifests(t, featureConfig),
		daprd.WithLogLineStdout(f.log),
	)

	return []framework.Option{
		framework.WithProcesses(f.sched, f.place, srv, f.log, f.daprd),
	}
}

func (f *fallback) Run(t *testing.T, ctx context.Context) {
	f.sched.WaitUntilRunning(t, ctx)
	f.place.WaitUntilRunning(t, ctx)
	f.daprd.WaitUntilRunning(t, ctx)

	f.log.EventuallyFoundAll(t)

	gclient := f.daprd.GRPCClient(t, ctx)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Method:    "foo",
		})
		assert.NoError(c, err)
	}, time.Second*10, time.Millisecond*10)
	assert.Positive(t, f.invoked.Load())
}
