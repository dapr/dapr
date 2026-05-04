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
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

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
	"github.com/dapr/durabletask-go/api/protos"
	dtclient "github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(localspoof))
}

// localspoof exercises the user-facing InvokeActor (gRPC) and direct actor
// HTTP endpoint with crafted dapr-caller-app-id / dapr-caller-namespace
// headers. The local sidecar's policy denies its own appID and allows a
// distinct "trusted-app" identity, so a successful call would prove the caller
// had spoofed identity. Both paths must strip the headers so the router stamps
// the trusted local appID instead.
type localspoof struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	db     *sqlite.SQLite
	daprd  *daprd.Daprd
}

func (l *localspoof) Setup(t *testing.T) []framework.Option {
	l.sentry = sentry.New(t)
	l.place = placement.New(t, placement.WithSentry(t, l.sentry))
	l.sched = scheduler.New(t, scheduler.WithSentry(l.sentry), scheduler.WithID("dapr-scheduler-server-0"))
	l.db = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: localspoofconfig
spec:
  features:
  - name: WorkflowAccessPolicy
    enabled: true`), 0o600))

	// The local daprd's appID is "localspoof-app". The policy grants
	// "trusted-app" full access and explicitly denies "localspoof-app" so the
	// test can detect a successful spoof: if the caller-identity headers were
	// honored, the policy would allow as "trusted-app".
	policy := []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: localspoof-test
scopes:
- localspoof-app
spec:
  defaultAction: deny
  rules:
  - callers:
    - appID: trusted-app
    workflows:
    - name: "*"
      operations: [schedule, terminate, raise, pause, resume, purge, get, rerun]
      action: allow
  - callers:
    - appID: localspoof-app
    workflows:
    - name: "*"
      operations: [schedule]
      action: deny
`)

	resDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(resDir, "policy.yaml"), policy, 0o600))

	l.daprd = daprd.New(t,
		daprd.WithAppID("localspoof-app"),
		daprd.WithNamespace("default"),
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(resDir),
		daprd.WithResourceFiles(l.db.GetComponent(t)),
		daprd.WithPlacementAddresses(l.place.Address()),
		daprd.WithSchedulerAddresses(l.sched.Address()),
		daprd.WithSentry(t, l.sentry),
	)

	return []framework.Option{
		framework.WithProcesses(l.sentry, l.place, l.sched, l.db, l.daprd),
	}
}

func (l *localspoof) Run(t *testing.T, ctx context.Context) {
	l.place.WaitUntilRunning(t, ctx)
	l.sched.WaitUntilRunning(t, ctx)
	l.daprd.WaitUntilRunning(t, ctx)

	registry := task.NewTaskRegistry()
	require.NoError(t, registry.AddWorkflowN("SpoofWF", func(ctx *task.WorkflowContext) (any, error) {
		return "ok", nil
	}))
	dtClient := dtclient.NewTaskHubGrpcClient(l.daprd.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, dtClient.StartWorkItemListener(ctx, registry))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(l.daprd.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	craftedPayload, err := proto.Marshal(&protos.CreateWorkflowInstanceRequest{
		StartEvent: &protos.HistoryEvent{
			EventType: &protos.HistoryEvent_ExecutionStarted{
				ExecutionStarted: &protos.ExecutionStartedEvent{
					Name: "SpoofWF",
					WorkflowInstance: &protos.WorkflowInstance{
						InstanceId: "spoof-instance",
					},
				},
			},
		},
	})
	require.NoError(t, err)

	daprClient := runtimev1pb.NewDaprClient(l.daprd.GRPCConn(t, ctx))
	workflowActorType := "dapr.internal.default.localspoof-app.workflow"

	t.Run("gRPC InvokeActor with spoofed dapr-caller-app-id is denied", func(t *testing.T) {
		// A naive client sets the caller-identity headers in the InvokeActor
		// request metadata, claiming to be "trusted-app". The fix strips these
		// before dispatch so the router stamps the real local appID, which the
		// policy denies.
		_, err := daprClient.InvokeActor(ctx, &runtimev1pb.InvokeActorRequest{
			ActorType: workflowActorType,
			ActorId:   "spoof-instance-grpc",
			Method:    "CreateWorkflowInstance",
			Data:      craftedPayload,
			Metadata: map[string]string{
				"dapr-caller-app-id":    "trusted-app",
				"dapr-caller-namespace": "default",
			},
		})
		require.Error(t, err, "spoofed caller-identity headers must not bypass policy")
		assert.Contains(t, err.Error(), "access denied by workflow access policy")
	})

	t.Run("HTTP direct actor invocation with spoofed headers is denied", func(t *testing.T) {
		httpClient := client.HTTP(t)
		url := fmt.Sprintf("http://%s/v1.0/actors/%s/spoof-instance-http/method/CreateWorkflowInstance",
			l.daprd.HTTPAddress(),
			workflowActorType,
		)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(craftedPayload)))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/protobuf")
		req.Header.Set("dapr-caller-app-id", "trusted-app")
		req.Header.Set("dapr-caller-namespace", "default")

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.NotEqualf(t, http.StatusOK, resp.StatusCode, "spoofed headers must not bypass policy; body: %s", body)
		assert.Contains(t, string(body), "access denied by workflow access policy")
	})
}
