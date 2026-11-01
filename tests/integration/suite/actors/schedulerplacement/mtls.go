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
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(mtls))
}

// mtls tests that actors work with placement served by the scheduler when
// mTLS is enabled, including invocation from a daprd which hosts no actor
// types itself.
type mtls struct {
	host   *daprd.Daprd
	caller *daprd.Daprd
	sched  *scheduler.Scheduler
	sentry *sentry.Sentry

	invoked        atomic.Int64
	reminderCalled atomic.Int64
}

func (m *mtls) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/myactorid", func(w http.ResponseWriter, r *http.Request) {})
	handler.HandleFunc("/actors/myactortype/myactorid/method/remind/myreminder", func(w http.ResponseWriter, r *http.Request) {
		m.reminderCalled.Add(1)
	})
	handler.HandleFunc("/actors/myactortype/myactorid/method/foo", func(w http.ResponseWriter, r *http.Request) {
		m.invoked.Add(1)
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	m.sentry = sentry.New(t)
	m.sched = scheduler.New(t,
		scheduler.WithSentry(m.sentry),
		scheduler.WithID("dapr-scheduler-server-0"),
		scheduler.WithPlacementEnabled(true),
	)
	m.host = daprd.New(t,
		daprd.WithSentry(t, m.sentry),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(srv.Port()),
		daprd.WithScheduler(m.sched),
	)
	m.caller = daprd.New(t,
		daprd.WithSentry(t, m.sentry),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithScheduler(m.sched),
	)

	return []framework.Option{
		framework.WithProcesses(m.sentry, m.sched, srv, m.host, m.caller),
	}
}

func (m *mtls) Run(t *testing.T, ctx context.Context) {
	m.sched.WaitUntilRunning(t, ctx)
	m.host.WaitUntilRunning(t, ctx)
	m.caller.WaitUntilRunning(t, ctx)

	gclient := m.caller.GRPCClient(t, ctx)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Method:    "foo",
		})
		assert.NoError(c, err)
	}, time.Second*20, time.Millisecond*10)
	assert.Positive(t, m.invoked.Load())

	// Reminder registration is only accepted by a daprd hosting the type.
	_, err := m.host.GRPCClient(t, ctx).RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "myreminder",
		DueTime:   "0s",
	})
	require.NoError(t, err)

	assert.Eventually(t, func() bool {
		return m.reminderCalled.Load() >= 1
	}, time.Second*10, time.Millisecond*10)
}
