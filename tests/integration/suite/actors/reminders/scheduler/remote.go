/*
Copyright 2024 The Dapr Authors
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

package scheduler

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	procscheduler "github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(remote))
}

type remote struct {
	place          *placement.Placement
	scheduler      *procscheduler.Scheduler
	srv1, srv2     *prochttp.HTTP
	daprd1, daprd2 *daprd.Daprd

	daprd1called atomic.Uint64
	daprd2called atomic.Uint64

	actorIDsNum int
	actorIDs    []string

	lock         sync.Mutex
	methodCalled atomic.Value

	deleteCalled atomic.Int64
}

func (r *remote) Setup(t *testing.T) []framework.Option {
	// if runtime.GOOS == "windows" {
	// 	t.Skip("Flaky tests to fix before 1.15") // TODO: fix flaky tests before 1.15
	// }

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: schedulerreminders
spec:
  features:
  - name: SchedulerReminders
    enabled: true`), 0o600))

	r.actorIDsNum = 100
	r.methodCalled.Store(make([]string, 0, r.actorIDsNum))
	r.actorIDs = make([]string, r.actorIDsNum)
	for i := range r.actorIDsNum {
		uid, err := uuid.NewUUID()
		require.NoError(t, err)
		r.actorIDs[i] = uid.String()
	}

	newHTTP := func(called *atomic.Uint64) *prochttp.HTTP {
		handler := http.NewServeMux()
		handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"entities": ["myactortype"]}`))
		})
		handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		for _, id := range r.actorIDs {
			handler.HandleFunc("/actors/myactortype/"+id, func(w http.ResponseWriter, req *http.Request) {
				if req.Method != http.MethodDelete {
					return
				}
				r.deleteCalled.Add(1)
			})
			handler.HandleFunc(fmt.Sprintf("/actors/myactortype/%s/method/remind/remindermethod", id), func(http.ResponseWriter, *http.Request) {
				r.lock.Lock()
				defer r.lock.Unlock()
				r.methodCalled.Store(append(r.methodCalled.Load().([]string), id))
				called.Add(1)
			})
			handler.HandleFunc(fmt.Sprintf("/actors/myactortype/%s/method/foo", id), func(http.ResponseWriter, *http.Request) {})
		}

		return prochttp.New(t, prochttp.WithHandler(handler))
	}

	r.scheduler = procscheduler.New(t)
	r.place = placement.New(t)

	r.srv1 = newHTTP(&r.daprd1called)
	r.srv2 = newHTTP(&r.daprd2called)
	r.daprd1 = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(r.place.Address()),
		daprd.WithSchedulerAddresses(r.scheduler.Address()),
		daprd.WithAppPort(r.srv1.Port()),
	)
	r.daprd2 = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(r.place.Address()),
		daprd.WithSchedulerAddresses(r.scheduler.Address()),
		daprd.WithAppPort(r.srv2.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(r.scheduler, r.place),
	}
}

func (r *remote) Run(t *testing.T, ctx context.Context) {
	r.daprd1.Run(t, ctx)
	r.daprd2.Run(t, ctx)
	r.srv1.Run(t, ctx)
	r.srv2.Run(t, ctx)

	r.scheduler.WaitUntilRunning(t, ctx)
	r.place.WaitUntilRunning(t, ctx)
	r.daprd1.WaitUntilRunning(t, ctx)
	r.daprd2.WaitUntilRunning(t, ctx)

	gclient := r.daprd1.GRPCClient(t, ctx)
	for _, id := range r.actorIDs {
		_, err := gclient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
			ActorType: "myactortype",
			ActorId:   id,
			Name:      "remindermethod",
			DueTime:   "1s",
			Data:      []byte("reminderdata"),
		})
		require.NoError(t, err)
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		r.lock.Lock()
		defer r.lock.Unlock()
		assert.Len(c, r.methodCalled.Load().([]string), r.actorIDsNum)
	}, time.Second*5, time.Millisecond*10)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.ElementsMatch(c, r.actorIDs, r.methodCalled.Load().([]string))
	}, time.Second*10, time.Millisecond*10)

	assert.GreaterOrEqual(t, r.daprd1called.Load(), uint64(0))
	assert.GreaterOrEqual(t, r.daprd2called.Load(), uint64(0))

	r.daprd2.Cleanup(t)
	r.daprd1.Cleanup(t)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(r.actorIDsNum), r.deleteCalled.Load())
	}, time.Second*20, 200*time.Millisecond)

	r.srv2.Cleanup(t)
	r.srv1.Cleanup(t)
}
