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

package requires

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(local)) //nolint:gocritic
}

// A single daprd hosts both the workflow and the gated activity. The policy
// is scoped to the daprd's own app ID.
//
// verify the following for same-app callers:
//  1. requires + propagated history = activity executes;
//  2. requires + missing event in propagated history = activity is denied;
//  3. requires + no propagation at all = activity is denied (the policy
//     checks the *propagated* history attached to the call, not the
//     workflow's local history).
type local struct {
	place *placement.Placement
	sched *scheduler.Scheduler
	db    *sqlite.SQLite
	dapr  *daprd.Daprd

	gatedRuns atomic.Int64
}

func (r *local) Setup(t *testing.T) []framework.Option {
	r.place = placement.New(t)
	r.sched = scheduler.New(t)
	r.db = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: wfacllocalconfig
spec:
  features:
  - name: WorkflowAccessPolicy
    enabled: true
`), 0o600))

	// Self-scoped policy: the daprd is BOTH the caller and the target.
	policy := []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: requires-local-test
scopes:
- wfacl-local
spec:
  defaultAction: deny
  rules:
  - callers:
    - appID: wfacl-local
    operations:
    - type: workflow
      name: "*"
      action: allow
    - type: activity
      name: FraudCheckPassed
      action: allow
    - type: activity
      name: HumanApprovalReceived
      action: allow
    - type: activity
      name: ProcessPayment
      action: allow
      requires:
      - eventType: activity
        status: Completed
        name: FraudCheckPassed
      - eventType: activity
        status: Completed
        name: HumanApprovalReceived
`)

	resDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(resDir, "policy.yaml"), policy, 0o600))

	r.dapr = daprd.New(t,
		daprd.WithAppID("wfacl-local"),
		daprd.WithNamespace("default"),
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(resDir),
		daprd.WithResourceFiles(r.db.GetComponent(t)),
		daprd.WithPlacementAddresses(r.place.Address()),
		daprd.WithSchedulerAddresses(r.sched.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(r.place, r.sched, r.db, r.dapr),
	}
}

func (r *local) Run(t *testing.T, ctx context.Context) {
	r.place.WaitUntilRunning(t, ctx)
	r.sched.WaitUntilRunning(t, ctx)
	r.dapr.WaitUntilRunning(t, ctx)

	reg := task.NewTaskRegistry()

	// WF_FullHistory: completes both prereq activities & propagates
	// the resulting history into the gated ProcessPayment call.
	require.NoError(t, reg.AddWorkflowN("WF_FullHistory", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("FraudCheckPassed").Await(nil); err != nil {
			return nil, fmt.Errorf("FraudCheckPassed failed: %w", err)
		}
		if err := ctx.CallActivity("HumanApprovalReceived").Await(nil); err != nil {
			return nil, fmt.Errorf("HumanApprovalReceived failed: %w", err)
		}
		var out string
		if err := ctx.CallActivity("ProcessPayment",
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("ProcessPayment failed: %w", err)
		}
		return out, nil
	}))

	// WF_PartialHistory: only completes 1/2 prereqs
	require.NoError(t, reg.AddWorkflowN("WF_PartialHistory", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("FraudCheckPassed").Await(nil); err != nil {
			return nil, fmt.Errorf("FraudCheckPassed failed: %w", err)
		}
		var out string
		if err := ctx.CallActivity("ProcessPayment",
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("ProcessPayment failed: %w", err)
		}
		return out, nil
	}))

	// WF_NoPropagation: completes both prereqs but omits
	// PropagateOwnHistory on the gated call. The policy must still deny
	// because it checks the propagated history attached to the request,
	// not the workflow's local history.
	require.NoError(t, reg.AddWorkflowN("WF_NoPropagation", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("FraudCheckPassed").Await(nil); err != nil {
			return nil, fmt.Errorf("FraudCheckPassed failed: %w", err)
		}
		if err := ctx.CallActivity("HumanApprovalReceived").Await(nil); err != nil {
			return nil, fmt.Errorf("HumanApprovalReceived failed: %w", err)
		}
		var out string
		if err := ctx.CallActivity("ProcessPayment").Await(&out); err != nil {
			return nil, fmt.Errorf("ProcessPayment failed: %w", err)
		}
		return out, nil
	}))

	require.NoError(t, reg.AddActivityN("FraudCheckPassed", func(ctx task.ActivityContext) (any, error) {
		return "fraud-ok", nil
	}))
	require.NoError(t, reg.AddActivityN("HumanApprovalReceived", func(ctx task.ActivityContext) (any, error) {
		return "approved", nil
	}))
	require.NoError(t, reg.AddActivityN("ProcessPayment", func(ctx task.ActivityContext) (any, error) {
		r.gatedRuns.Add(1)
		return "payment-processed", nil
	}))

	bclient := client.NewTaskHubGrpcClient(r.dapr.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, bclient.StartWorkItemListener(ctx, reg))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(r.dapr.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, 20*time.Second, 10*time.Millisecond)

	t.Run("allowed when prerequisites are propagated to the gated activity", func(t *testing.T) {
		before := r.gatedRuns.Load()

		id, err := bclient.ScheduleNewWorkflow(ctx, "WF_FullHistory")
		require.NoError(t, err)

		metadata, err := bclient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.True(t, api.WorkflowMetadataIsComplete(metadata))
		assert.Nil(t, metadata.GetFailureDetails(),
			"WF_FullHistory should succeed: %v", metadata.GetFailureDetails())
		assert.Equal(t, `"payment-processed"`, metadata.GetOutput().GetValue())
		assert.Equal(t, before+1, r.gatedRuns.Load(),
			"ProcessPayment should have run exactly once locally")
	})

	t.Run("denied when one required activity is missing from propagated history", func(t *testing.T) {
		before := r.gatedRuns.Load()

		id, err := bclient.ScheduleNewWorkflow(ctx, "WF_PartialHistory")
		require.NoError(t, err)

		metadata, err := bclient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails(),
			"workflow should fail when a required activity is missing from propagated history")
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(),
			"required history not satisfied",
			"local enforcement should surface the requires-unmet message")
		assert.Equal(t, before, r.gatedRuns.Load(),
			"ProcessPayment must NOT have run when access is denied")
	})

	t.Run("denied when caller does not propagate history", func(t *testing.T) {
		before := r.gatedRuns.Load()

		id, err := bclient.ScheduleNewWorkflow(ctx, "WF_NoPropagation")
		require.NoError(t, err)

		metadata, err := bclient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails(),
			"workflow should fail when caller does not propagate history, even with the prerequisites in local history")
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(),
			"required history not satisfied")
		assert.Equal(t, before, r.gatedRuns.Load(),
			"ProcessPayment must NOT have run when access is denied")
	})
}
