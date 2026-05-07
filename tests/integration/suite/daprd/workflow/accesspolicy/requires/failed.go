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
	"errors"
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
	suite.Register(new(failed)) //nolint:gocritic
}

// failed verifies the compensation pattern enabled by
// `status: Failed` requires entries: a `Compensate` activity may only run
// when a specific upstream activity (`ChargeCard`) has failed in the
// caller's propagated history.
//
// Three sub-tests cover the matrix:
//  1. ChargeCard FAILED → Compensate is allowed (the typical compensation flow).
//  2. ChargeCard SUCCEEDED → Compensate is denied (no Failed event in history).
//  3. ChargeCard not called at all → Compensate is denied.
type failed struct {
	place *placement.Placement
	sched *scheduler.Scheduler
	db    *sqlite.SQLite
	dapr  *daprd.Daprd

	// chargeCardShouldFail flips the behavior of the ChargeCard activity
	// for the current sub-test
	chargeCardShouldFail atomic.Bool

	// compensateRuns counts how many times the gated Compensate activity
	// actually executed. Used to assert that denied calls never reach the
	// activity body.
	compensateRuns atomic.Int64
}

func (r *failed) Setup(t *testing.T) []framework.Option {
	r.place = placement.New(t)
	r.sched = scheduler.New(t)
	r.db = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: wfaclfailedconfig
spec:
  features:
  - name: WorkflowAccessPolicy
    enabled: true
`), 0o600))

	// Self-scoped policy: Compensate is gated on a Failed event for
	// ChargeCard. ChargeCard itself is allowed unconditionally so the
	// workflow can run it (and possibly fail it) to build the history
	// the policy will then evaluate.
	policy := []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: requires-failed-test
scopes:
- wfacl-failed
spec:
  defaultAction: deny
  rules:
  - callers:
    - appID: wfacl-failed
    operations:
    - type: workflow
      name: "*"
      action: allow
    - type: activity
      name: ChargeCard
      action: allow
    - type: activity
      name: Compensate
      action: allow
      requires:
      - eventType: activity
        status: Failed
        name: ChargeCard
`)

	resDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(resDir, "policy.yaml"), policy, 0o600))

	r.dapr = daprd.New(t,
		daprd.WithAppID("wfacl-failed"),
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

func (r *failed) Run(t *testing.T, ctx context.Context) {
	r.place.WaitUntilRunning(t, ctx)
	r.sched.WaitUntilRunning(t, ctx)
	r.dapr.WaitUntilRunning(t, ctx)

	reg := task.NewTaskRegistry()

	// WF_ChargeThenCompensate calls ChargeCard, then ALWAYS calls
	// Compensate (with PropagateOwnHistory). The gating decision depends
	// solely on whether ChargeCard's history event was a TaskCompleted or
	// TaskFailed, which is controlled by the `chargeCardShouldFail` flag
	// flipped per sub-test. The workflow swallows ChargeCard's failure so
	// the test can observe Compensate's outcome on its own.
	require.NoError(t, reg.AddWorkflowN("WF_ChargeThenCompensate", func(ctx *task.WorkflowContext) (any, error) {
		// Intentionally don't return early on ChargeCard's error — we want
		// to observe Compensate's gating decision regardless.
		_ = ctx.CallActivity("ChargeCard").Await(nil)

		var out string
		if err := ctx.CallActivity("Compensate",
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("Compensate failed: %w", err)
		}
		return out, nil
	}))

	// WF_CompensateOnly skips ChargeCard entirely, then calls Compensate.
	// Without ChargeCard's TaskFailed in history, the requires can't be
	// satisfied = deny
	require.NoError(t, reg.AddWorkflowN("WF_CompensateOnly", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallActivity("Compensate",
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("Compensate failed: %w", err)
		}
		return out, nil
	}))

	require.NoError(t, reg.AddActivityN("ChargeCard", func(ctx task.ActivityContext) (any, error) {
		if r.chargeCardShouldFail.Load() {
			return nil, errors.New("charge declined")
		}
		return "charged", nil
	}))

	require.NoError(t, reg.AddActivityN("Compensate", func(ctx task.ActivityContext) (any, error) {
		r.compensateRuns.Add(1)
		return "refunded", nil
	}))

	bclient := client.NewTaskHubGrpcClient(r.dapr.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, bclient.StartWorkItemListener(ctx, reg))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(r.dapr.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, 20*time.Second, 10*time.Millisecond)

	t.Run("allowed: ChargeCard failed in propagated history = Compensate runs", func(t *testing.T) {
		r.chargeCardShouldFail.Store(true)
		before := r.compensateRuns.Load()

		id, err := bclient.ScheduleNewWorkflow(ctx, "WF_ChargeThenCompensate")
		require.NoError(t, err)

		metadata, err := bclient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.True(t, api.WorkflowMetadataIsComplete(metadata))
		assert.Nil(t, metadata.GetFailureDetails(),
			"Compensate should be allowed when its required Failed event is in history: %v",
			metadata.GetFailureDetails())
		assert.Equal(t, `"refunded"`, metadata.GetOutput().GetValue())
		assert.Equal(t, before+1, r.compensateRuns.Load(),
			"Compensate body should have executed exactly once")
	})

	t.Run("denied: ChargeCard succeeded = no Failed event & Compensate is denied", func(t *testing.T) {
		r.chargeCardShouldFail.Store(false)
		before := r.compensateRuns.Load()

		id, err := bclient.ScheduleNewWorkflow(ctx, "WF_ChargeThenCompensate")
		require.NoError(t, err)

		metadata, err := bclient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails(),
			"workflow should fail when Compensate is invoked without a Failed event for ChargeCard")
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(),
			"required history not satisfied",
			"Compensate without a matching Failed event must surface the requires-unmet message")
		assert.Equal(t, before, r.compensateRuns.Load(),
			"Compensate must NOT execute when its requires block is unmet")
	})

	t.Run("denied: ChargeCard never called = Compensate is denied", func(t *testing.T) {
		r.chargeCardShouldFail.Store(false)
		before := r.compensateRuns.Load()

		id, err := bclient.ScheduleNewWorkflow(ctx, "WF_CompensateOnly")
		require.NoError(t, err)

		metadata, err := bclient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails(),
			"workflow should fail when ChargeCard was never even scheduled")
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(),
			"required history not satisfied")
		assert.Equal(t, before, r.compensateRuns.Load(),
			"Compensate must NOT execute when its prerequisite was never run")
	})
}
