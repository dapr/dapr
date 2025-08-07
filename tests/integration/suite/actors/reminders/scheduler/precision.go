/*
Copyright 2025 The Dapr Authors
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
	"net/http"
	"os"
	"path/filepath"
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
	"github.com/dapr/kit/concurrency/slice"
)

func init() {
	suite.Register(new(precision))
}

type precision struct {
	place     *placement.Placement
	scheduler *scheduler.Scheduler
	daprd     *daprd.Daprd
	called    slice.Slice[*request]
}

type request struct {
	name          string
	executionTime time.Time
}

func (r *precision) Setup(t *testing.T) []framework.Option {
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

	r.called = slice.New[*request]()

	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/myactorid", func(http.ResponseWriter, *http.Request) {
	})
	handler.HandleFunc("/actors/myactortype/myactorid/method/remind/sec", func(http.ResponseWriter, *http.Request) {
		r.called.Append(&request{
			name:          "sec",
			executionTime: time.Now(),
		})
	})
	handler.HandleFunc("/actors/myactortype/myactorid/method/remind/ms", func(http.ResponseWriter, *http.Request) {
		r.called.Append(&request{
			name:          "ms",
			executionTime: time.Now(),
		})
	})

	r.scheduler = scheduler.New(t)
	srv := prochttp.New(t, prochttp.WithHandler(handler))
	r.place = placement.New(t)
	r.daprd = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(r.place.Address()),
		daprd.WithSchedulerAddresses(r.scheduler.Address()),
		daprd.WithAppPort(srv.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(r.scheduler, r.place, srv, r.daprd),
	}
}

func (r *precision) Run(t *testing.T, ctx context.Context) {
	r.scheduler.WaitUntilRunning(t, ctx)
	r.place.WaitUntilRunning(t, ctx)
	r.daprd.WaitUntilRunning(t, ctx)

	gclient := r.daprd.GRPCClient(t, ctx)
	_, err := gclient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "sec",
		Data:      []byte("reminderdata"),
		Period:    "1s",
		Ttl:       "5s",
	})
	require.NoError(t, err)

	_, err = gclient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "ms",
		Data:      []byte("reminderdata"),
		Period:    "1ms",
		Ttl:       "10ms",
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, r.called.Slice(), 15)
	}, time.Second*10, time.Millisecond*10)

	eMap := make(map[string][]*request)
	for _, v := range r.called.Slice() {
		eMap[v.name] = append(eMap[v.name], v)
	}

	tolerance := 500 * time.Millisecond
	assertDurationWithTolerance(t, eMap, "sec", 1*time.Second, float64(tolerance))
	assertDurationWithTolerance(t, eMap, "ms", time.Millisecond, float64(tolerance))
}

func assertDurationWithTolerance(t *testing.T, values map[string][]*request, key string, expectedDiff time.Duration, tolerance float64) {
	var prevTime time.Time
	for i, e := range values[key] {
		if prevTime.IsZero() {
			prevTime = e.executionTime
			continue
		}

		actualDiff := e.executionTime.Sub(prevTime)

		// Check if the difference is within tolerance
		assert.InDeltaf(t, expectedDiff, actualDiff, tolerance, "[%v]Expected execution time difference to be %v ± %v, but got %v", i,
			expectedDiff, tolerance, actualDiff)

		prevTime = e.executionTime
	}
}
