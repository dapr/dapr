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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(reloadenforced))
}

type reloadenforced struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	db     *sqlite.SQLite
	caller *daprd.Daprd
	target *daprd.Daprd
	resDir string
}

const (
	reloadTargetAppID = "reload-target"
	reloadCallerAppID = "reload-caller"
)

func (r *reloadenforced) Setup(t *testing.T) []framework.Option {
	r.sentry = sentry.New(t)
	r.place = placement.New(t, placement.WithSentry(t, r.sentry))
	r.sched = scheduler.New(t, scheduler.WithSentry(r.sentry), scheduler.WithID("dapr-scheduler-server-0"))
	r.db = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: reloadconfig
spec:
  features:
  - name: HotReload
    enabled: true`), 0o600))

	r.resDir = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(r.resDir, "reload-init.yaml"), r.policy("reload-init", "schedule"), 0o600))

	r.caller = daprd.New(t,
		daprd.WithAppID(reloadCallerAppID),
		daprd.WithNamespace("default"),
		daprd.WithConfigs(configFile),
		daprd.WithResourceFiles(r.db.GetComponent(t)),
		daprd.WithPlacementAddresses(r.place.Address()),
		daprd.WithSchedulerAddresses(r.sched.Address()),
		daprd.WithSentry(t, r.sentry),
	)
	r.target = daprd.New(t,
		daprd.WithAppID(reloadTargetAppID),
		daprd.WithNamespace("default"),
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(r.resDir),
		daprd.WithResourceFiles(r.db.GetComponent(t)),
		daprd.WithPlacementAddresses(r.place.Address()),
		daprd.WithSchedulerAddresses(r.sched.Address()),
		daprd.WithSentry(t, r.sentry),
	)

	return []framework.Option{
		framework.WithProcesses(r.sentry, r.place, r.sched, r.db, r.caller, r.target),
	}
}

func (r *reloadenforced) policy(name, op string) []byte {
	return []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: ` + name + `
scopes:
- ` + reloadTargetAppID + `
spec:
  rules:
  - callers:
    - appID: ` + reloadCallerAppID + `
    workflows:
    - name: "ReloadWF"
      operations: [` + op + `]
`)
}

func (r *reloadenforced) Run(t *testing.T, ctx context.Context) {
	r.place.WaitUntilRunning(t, ctx)
	r.sched.WaitUntilRunning(t, ctx)
	r.caller.WaitUntilRunning(t, ctx)
	r.target.WaitUntilRunning(t, ctx)

	reg := task.NewTaskRegistry()
	require.NoError(t, reg.AddWorkflowN("ReloadWF", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.WaitForSingleEvent("done", time.Hour).Await(nil)
	}))
	targetClient := client.NewTaskHubGrpcClient(r.target.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, targetClient.StartWorkItemListener(ctx, reg))
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(r.target.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	callerActorClient := runtimev1pb.NewDaprClient(r.caller.GRPCConn(t, ctx))
	targetActorType := "dapr.internal.default." + reloadTargetAppID + ".workflow"

	for i := range 30 {
		allow := i%2 == 1
		op := "terminate"
		if allow {
			op = "schedule"
		}
		name := fmt.Sprintf("reload-%d", i)

		entries, err := os.ReadDir(r.resDir)
		require.NoError(t, err)
		for _, e := range entries {
			if filepath.Ext(e.Name()) == ".yaml" {
				require.NoError(t, os.Remove(filepath.Join(r.resDir, e.Name())))
			}
		}
		require.NoError(t, os.WriteFile(filepath.Join(r.resDir, name+".yaml"), r.policy(name, op), 0o600))
		require.Eventually(t, func() bool {
			policies := r.target.GetMetadata(t, ctx).WorkflowAccessPolicies
			return len(policies) == 1 && policies[0].GetName() == name
		}, time.Second*20, time.Microsecond)

		id := fmt.Sprintf("reload-wf-%d", i)
		_, err = callerActorClient.InvokeActor(ctx, &runtimev1pb.InvokeActorRequest{
			ActorType: targetActorType,
			ActorId:   id,
			Method:    "CreateWorkflowInstance",
			Data:      mustMarshalCreate(t, "ReloadWF", id),
		})
		if allow {
			require.NoError(t, err, "round %d: policy %s allows schedule", i, name)
		} else {
			require.Error(t, err, "round %d: policy %s listed by metadata must already deny schedule", i, name)
			assert.Contains(t, err.Error(), "access denied by workflow access policy")
		}
	}
}
