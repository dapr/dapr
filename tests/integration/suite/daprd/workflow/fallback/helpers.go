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

package fallback

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/client"
)

// fallbackSettle is the settle period the tests give their daprds in place
// of the default, so a daprd without a worker turns into a fallback host soon
// after the placement tables show that no daprd of the app has one.
const fallbackSettle = time.Second

// withSettle gives the daprd at index the tests' settle period.
func withSettle(t *testing.T, index int) workflow.Option {
	return workflow.WithDaprdOptions(index, daprd.WithWorkflowFallbackSettle(t, fallbackSettle))
}

// newPair returns two daprds of one app with staged operations and the given
// fallback settle period, the second with the given fallback damping period,
// in place of the defaults, and opts applied on top.
func newPair(t *testing.T, damping, settle time.Duration, opts ...workflow.Option) *workflow.Workflow {
	t.Helper()

	appID := uuid.NewString()
	return workflow.New(t, append([]workflow.Option{
		workflow.WithDaprds(2),
		workflow.WithStagedOperations(),
		workflow.WithDaprdOptions(0, daprd.WithAppID(appID), daprd.WithWorkflowFallbackSettle(t, settle)),
		workflow.WithDaprdOptions(1, daprd.WithAppID(appID), daprd.WithWorkflowFallbackSettle(t, settle), daprd.WithWorkflowFallbackDamping(t, damping)),
	}, opts...)...)
}

// placementBack returns a placement service, not yet running, which takes
// over the identity and address of wf's placement service once that one is
// stopped, so the daprds reconnect to it. Start it with runPlacementBack. wf
// must pin the placement service with workflow.WithPlacementService.
func placementBack(t *testing.T, wf *workflow.Workflow) *placement.Placement {
	t.Helper()

	p := wf.Placement()
	return placement.New(t,
		placement.WithID(p.ID()),
		placement.WithPort(p.Port()),
		placement.WithHealthzPort(p.HealthzPort()),
		placement.WithMetricsPort(p.MetricsPort()),
		placement.WithInitialCluster(p.InitialCluster()),
		placement.WithInitialClusterPorts(p.InitialClusterPorts()...),
	)
}

// runPlacementBack runs a placement service from placementBack and waits until
// it is healthy. Its cleanup is registered here, so it runs before the test
// case's context is cancelled, which would kill the process.
func runPlacementBack(t *testing.T, ctx context.Context, back *placement.Placement) {
	t.Helper()

	back.Run(t, ctx)
	t.Cleanup(func() { back.Cleanup(t) })
	back.WaitUntilRunning(t, ctx)
}

// placementStatus returns the placement connection status the daprd at index
// reports, such as "placement: connected". A failed metadata read fails c and
// returns "".
func placementStatus(c assert.TestingT, ctx context.Context, wf *workflow.Workflow, index int) string {
	rt := wf.DaprN(index).GetMetaActorRuntime(c, ctx)
	if rt == nil {
		return ""
	}
	return rt.Placement
}

// reminderDeliveries returns how many actor reminders the scheduler delivered
// to the daprd at index, of any actor type and whatever their outcome: the
// daprd counts each job it receives on its scheduler stream for an actor
// reminder, before it routes the call to the actor's owner. A failed metrics
// read fails c.
func reminderDeliveries(c assert.TestingT, ctx context.Context, wf *workflow.Workflow, index int) float64 {
	return wf.DaprN(index).Metrics(c, ctx).SumWithLabels("dapr_runtime_actor_reminders_fired_total")
}

// hostsWorkflows returns whether the daprd at index hosts the workflow actor
// type, as a fallback host or with a worker. A failed metadata read fails c
// and returns false.
func hostsWorkflows(c assert.TestingT, ctx context.Context, wf *workflow.Workflow, index int) bool {
	_, hosted := wf.DaprN(index).ActiveActorCount(c, ctx, wf.WorkflowActorType(index))
	return hosted
}

// waitCompleted waits for the workflow to complete and returns its metadata.
func waitCompleted(t *testing.T, ctx context.Context, c *client.TaskHubGrpcClient, id api.InstanceID) *backend.WorkflowMetadata {
	t.Helper()

	meta, err := c.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err, id)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus(), id)
	return meta
}

// assertCompleted waits for every workflow to complete.
func assertCompleted(t *testing.T, ctx context.Context, c *client.TaskHubGrpcClient, ids ...api.InstanceID) {
	t.Helper()

	for _, id := range ids {
		waitCompleted(t, ctx, c, id)
	}
}

// startConcurrently runs body in a loop on each of n goroutines, with the
// goroutine and the iteration numbers, until the returned stop is called.
// When body returns true, the instance ID it returns is recorded; stop waits
// for the goroutines and returns the recorded IDs.
func startConcurrently(n int, body func(g, i int) (api.InstanceID, bool)) (stop func() []api.InstanceID) {
	done := make(chan struct{})
	var wg sync.WaitGroup
	var lock sync.Mutex
	var ids []api.InstanceID
	for g := range n {
		wg.Go(func() {
			for i := 0; ; i++ {
				select {
				case <-done:
					return
				default:
				}

				if id, ok := body(g, i); ok {
					lock.Lock()
					ids = append(ids, id)
					lock.Unlock()
				}
			}
		})
	}

	return func() []api.InstanceID {
		close(done)
		wg.Wait()
		return ids
	}
}

// readErrors records whether a read failed, for a background poll which
// must not fail the test on a read cut short by its own end.
type readErrors struct{ failed bool }

func (r *readErrors) Errorf(string, ...any) { r.failed = true }
