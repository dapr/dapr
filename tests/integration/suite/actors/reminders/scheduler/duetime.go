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
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(duetime))
}

type duetime struct {
	daprd *daprd.Daprd
	place *placement.Placement

	reminderCalled     atomic.Int64
	stopReminderCalled atomic.Int64
}

func (d *duetime) Setup(t *testing.T) []framework.Option {
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

	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/myactorid/method/remind/remindermethod", func(w http.ResponseWriter, r *http.Request) {
		d.reminderCalled.Add(1)
	})
	handler.HandleFunc("/actors/myactortype/myactorid/method/remind/stopreminder", func(w http.ResponseWriter, r *http.Request) {
		d.stopReminderCalled.Add(1)
		w.Header().Set("X-DaprReminderCancel", "true")
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/myactorid/method/foo", func(w http.ResponseWriter, r *http.Request) {})

	scheduler := scheduler.New(t)
	srv := prochttp.New(t, prochttp.WithHandler(handler))
	d.place = placement.New(t)
	d.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(d.place.Address()),
		daprd.WithAppPort(srv.Port()),
		daprd.WithSchedulerAddresses(scheduler.Address()),
		daprd.WithConfigs(configFile),
	)

	return []framework.Option{
		framework.WithProcesses(scheduler, d.place, srv, d.daprd),
	}
}

func (d *duetime) Run(t *testing.T, ctx context.Context) {
	d.place.WaitUntilRunning(t, ctx)
	d.daprd.WaitUntilRunning(t, ctx)

	client := util.HTTPClient(t)

	daprdURL := "http://localhost:" + strconv.Itoa(d.daprd.HTTPPort()) + "/v1.0/actors/myactortype/myactorid"

	t.Run("actor ready", func(t *testing.T) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, daprdURL+"/method/foo", nil)
		require.NoError(t, err)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, rErr := client.Do(req)
			//nolint:testifylint
			if assert.NoError(c, rErr) {
				assert.NoError(c, resp.Body.Close())
				assert.Equal(c, http.StatusOK, resp.StatusCode)
			}
		}, 10*time.Second, 10*time.Millisecond, "actor not ready in time")
	})

	conn, err := grpc.DialContext(ctx, d.daprd.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	gclient := rtv1.NewDaprClient(conn)

	t.Run("schedule reminder via gRPC", func(t *testing.T) {
		_, err = gclient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Name:      "remindermethod",
			DueTime:   "0s",
			Period:    "PT1M",
		})
		require.NoError(t, err)

		assert.Eventually(t, func() bool {
			return d.reminderCalled.Load() == 1
		}, 3*time.Second, 10*time.Millisecond)
	})
}
