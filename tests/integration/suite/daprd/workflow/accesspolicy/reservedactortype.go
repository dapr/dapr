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

package accesspolicy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	dtclient "github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(reservedactortype))
}

// reservedactortype asserts that user-facing actor APIs reject calls targeting
// the Dapr-reserved "dapr.internal.*" actor types.
type reservedactortype struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	db     *sqlite.SQLite
	daprd  *daprd.Daprd
}

func (r *reservedactortype) Setup(t *testing.T) []framework.Option {
	r.sentry = sentry.New(t)
	r.place = placement.New(t, placement.WithSentry(t, r.sentry))
	r.sched = scheduler.New(t, scheduler.WithSentry(r.sentry), scheduler.WithID("dapr-scheduler-server-0"))
	r.db = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	r.daprd = daprd.New(t,
		daprd.WithAppID("reserved-app"),
		daprd.WithNamespace("default"),
		daprd.WithResourceFiles(r.db.GetComponent(t)),
		daprd.WithPlacementAddresses(r.place.Address()),
		daprd.WithSchedulerAddresses(r.sched.Address()),
		daprd.WithSentry(t, r.sentry),
	)

	return []framework.Option{
		framework.WithProcesses(r.sentry, r.place, r.sched, r.db, r.daprd),
	}
}

func (r *reservedactortype) Run(t *testing.T, ctx context.Context) {
	r.place.WaitUntilRunning(t, ctx)
	r.sched.WaitUntilRunning(t, ctx)
	r.daprd.WaitUntilRunning(t, ctx)

	// Register a workflow so the workflow actor types exist on this sidecar.
	registry := task.NewTaskRegistry()
	require.NoError(t, registry.AddWorkflowN("DummyWF", func(ctx *task.WorkflowContext) (any, error) {
		return "ok", nil
	}))
	dtClient := dtclient.NewTaskHubGrpcClient(r.daprd.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, dtClient.StartWorkItemListener(ctx, registry))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(r.daprd.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	daprClient := runtimev1pb.NewDaprClient(r.daprd.GRPCConn(t, ctx))
	httpClient := client.HTTP(t)

	for _, actorType := range []string{
		"dapr.internal.default.reserved-app.workflow",
		"dapr.internal.default.reserved-app.activity",
	} {
		t.Run(actorType, func(t *testing.T) {
			t.Run("gRPC ExecuteActorStateTransaction is denied", func(t *testing.T) {
				_, err := daprClient.ExecuteActorStateTransaction(ctx, &runtimev1pb.ExecuteActorStateTransactionRequest{
					ActorType: actorType,
					ActorId:   "any",
					Operations: []*runtimev1pb.TransactionalActorStateOperation{{
						OperationType: "upsert", Key: "k",
					}},
				})
				require.Error(t, err)
				assert.Contains(t, err.Error(), "reserved for the Dapr workflow runtime")
			})

			t.Run("gRPC GetActorState is denied", func(t *testing.T) {
				_, err := daprClient.GetActorState(ctx, &runtimev1pb.GetActorStateRequest{
					ActorType: actorType, ActorId: "any", Key: "k",
				})
				require.Error(t, err)
				assert.Contains(t, err.Error(), "reserved for the Dapr workflow runtime")
			})

			t.Run("gRPC RegisterActorReminder is denied", func(t *testing.T) {
				_, err := daprClient.RegisterActorReminder(ctx, &runtimev1pb.RegisterActorReminderRequest{
					ActorType: actorType,
					ActorId:   "any",
					Name:      "spoof",
					DueTime:   "1s",
				})
				require.Error(t, err)
				assert.Contains(t, err.Error(), "reserved for the Dapr workflow runtime")
			})

			t.Run("gRPC RegisterActorTimer is denied", func(t *testing.T) {
				_, err := daprClient.RegisterActorTimer(ctx, &runtimev1pb.RegisterActorTimerRequest{
					ActorType: actorType, ActorId: "any", Name: "spoof", DueTime: "1s",
				})
				require.Error(t, err)
				assert.Contains(t, err.Error(), "reserved for the Dapr workflow runtime")
			})

			t.Run("HTTP onActorStateTransaction is denied", func(t *testing.T) {
				url := fmt.Sprintf("http://%s/v1.0/actors/%s/any/state", r.daprd.HTTPAddress(), actorType)
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader([]byte(`[]`)))
				require.NoError(t, err)
				req.Header.Set("Content-Type", "application/json")
				resp, err := httpClient.Do(req)
				require.NoError(t, err)
				defer resp.Body.Close()
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				assert.Equal(t, http.StatusForbidden, resp.StatusCode, "body: %s", body)
				assert.Contains(t, string(body), "reserved for the Dapr workflow runtime")
			})

			t.Run("HTTP onGetActorState is denied", func(t *testing.T) {
				url := fmt.Sprintf("http://%s/v1.0/actors/%s/any/state/k", r.daprd.HTTPAddress(), actorType)
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
				require.NoError(t, err)
				resp, err := httpClient.Do(req)
				require.NoError(t, err)
				defer resp.Body.Close()
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				assert.Equal(t, http.StatusForbidden, resp.StatusCode, "body: %s", body)
				assert.Contains(t, string(body), "reserved for the Dapr workflow runtime")
			})

			t.Run("HTTP register reminder is denied", func(t *testing.T) {
				url := fmt.Sprintf("http://%s/v1.0/actors/%s/any/reminders/spoof", r.daprd.HTTPAddress(), actorType)
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader([]byte(`{"dueTime":"1s"}`)))
				require.NoError(t, err)
				req.Header.Set("Content-Type", "application/json")
				resp, err := httpClient.Do(req)
				require.NoError(t, err)
				defer resp.Body.Close()
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				assert.Equal(t, http.StatusForbidden, resp.StatusCode, "body: %s", body)
				assert.Contains(t, string(body), "reserved for the Dapr workflow runtime")
			})
		})
	}
}
