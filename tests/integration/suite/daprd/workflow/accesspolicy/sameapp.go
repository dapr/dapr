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

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(sameapp))
}

// sameapp validates that a WorkflowAccessPolicy applied to an app does not
// block the app's own workflow internals when the app is not listed as a
// caller in any rule. Specifically, parent/child workflow event delivery via
// CallActor (e.g. AddWorkflowEvent) routes through the gRPC ACL hook even for
// same-app calls; the hook must bypass the policy when the caller's
// mTLS-verified SPIFFE identity matches this app's identity.
type sameapp struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	db     *sqlite.SQLite
	app    *daprd.Daprd
}

func (s *sameapp) Setup(t *testing.T) []framework.Option {
	s.sentry = sentry.New(t)

	s.place = placement.New(t, placement.WithSentry(t, s.sentry))
	s.sched = scheduler.New(t, scheduler.WithSentry(s.sentry), scheduler.WithID("dapr-scheduler-server-0"))
	s.db = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: wfaclconfig
spec:
  features:
  - name: WorkflowAccessPolicy
    enabled: true`), 0o600))

	// Policy scoped to "sameapp-target" but with rules referencing only some
	// hypothetical *other* caller — never the target app itself.
	policy := []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: sameapp-policy
scopes:
- sameapp-target
spec:
  defaultAction: allow
  rules:
  - callers:
    - appID: some-other-app
    operations:
    - type: workflow
      name: "*"
      action: deny
`)

	resDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(resDir, "policy.yaml"), policy, 0o600))

	s.app = daprd.New(t,
		daprd.WithAppID("sameapp-target"),
		daprd.WithNamespace("default"),
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(resDir),
		daprd.WithResourceFiles(s.db.GetComponent(t)),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithSchedulerAddresses(s.sched.Address()),
		daprd.WithSentry(t, s.sentry),
	)

	return []framework.Option{
		framework.WithProcesses(s.sentry, s.place, s.sched, s.db, s.app),
	}
}

func (s *sameapp) Run(t *testing.T, ctx context.Context) {
	s.place.WaitUntilRunning(t, ctx)
	s.sched.WaitUntilRunning(t, ctx)
	s.app.WaitUntilRunning(t, ctx)

	reg := task.NewTaskRegistry()

	// Parent workflow calls a child workflow on the same app. The child
	// workflow's completion event flows back to the parent via
	// AddWorkflowEvent, which is the call that gets denied by IsCallerKnown
	// without the same-app bypass.
	require.NoError(t, reg.AddWorkflowN("Parent", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallChildWorkflow("Child").Await(&out); err != nil {
			return nil, fmt.Errorf("child workflow failed: %w", err)
		}
		return out, nil
	}))

	require.NoError(t, reg.AddWorkflowN("Child", func(ctx *task.WorkflowContext) (any, error) {
		return "child-done", nil
	}))

	appClient := client.NewTaskHubGrpcClient(s.app.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, appClient.StartWorkItemListener(ctx, reg))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(s.app.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	id, err := appClient.ScheduleNewWorkflow(ctx, "Parent")
	require.NoError(t, err)

	completionCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	t.Cleanup(cancel)
	metadata, err := appClient.WaitForWorkflowCompletion(completionCtx, id, api.WithFetchPayloads(true))
	require.NoError(t, err, "parent/child workflow should complete; if it hangs, the same-app ACL bypass regressed")
	require.Nil(t, metadata.GetFailureDetails(),
		"parent workflow should not fail under a policy that doesn't list the app as a caller")
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))
	assert.Equal(t, `"child-done"`, metadata.GetOutput().GetValue())
}
