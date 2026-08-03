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
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(basic))
}

// basic tests that actors and reminders work with placement served by the
// scheduler, with no standalone placement service running.
type basic struct {
	daprd *daprd.Daprd
	sched *scheduler.Scheduler

	invoked        atomic.Int64
	reminderCalled atomic.Int64
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/myactorid", func(w http.ResponseWriter, r *http.Request) {})
	handler.HandleFunc("/actors/myactortype/myactorid/method/remind/myreminder", func(w http.ResponseWriter, r *http.Request) {
		b.reminderCalled.Add(1)
	})
	handler.HandleFunc("/actors/myactortype/myactorid/method/foo", func(w http.ResponseWriter, r *http.Request) {
		b.invoked.Add(1)
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	b.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))
	b.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(srv.Port()),
		daprd.WithScheduler(b.sched),
		daprd.WithConfigManifests(t, featureConfig),
	)

	return []framework.Option{
		framework.WithProcesses(b.sched, srv, b.daprd),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	b.sched.WaitUntilRunning(t, ctx)
	// daprd only becomes ready once it has a placement table from the
	// scheduler for every actor type it hosts.
	b.daprd.WaitUntilRunning(t, ctx)

	gclient := b.daprd.GRPCClient(t, ctx)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Method:    "foo",
		})
		assert.NoError(c, err)
	}, time.Second*10, time.Millisecond*10)
	assert.Positive(t, b.invoked.Load())

	_, err := gclient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "myreminder",
		DueTime:   "0s",
	})
	require.NoError(t, err)

	assert.Eventually(t, func() bool {
		return b.reminderCalled.Load() >= 1
	}, time.Second*10, time.Millisecond*10)
}
