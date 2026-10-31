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

package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/client"
)

// FallbackSettle is a short settle period for fallback tests to give their
// daprds in place of the default, so a daprd without a worker turns into a
// fallback host soon after the placement tables show that no daprd of the app
// has one.
const FallbackSettle = time.Second

// DoneOutput is what fallback tests' workflows return once they complete.
const DoneOutput = "done"

// WithFallbackSettleN gives the daprd at index the FallbackSettle settle
// period.
func WithFallbackSettleN(t *testing.T, index int) Option {
	return WithDaprdOptions(index, daprd.WithWorkflowFallbackSettle(t, FallbackSettle))
}

// WithFallbackPair runs two daprds of one app with staged operations and the
// given fallback settle period, the second with the given fallback damping
// period, in place of the defaults.
func WithFallbackPair(t *testing.T, damping, settle time.Duration) Option {
	appID := uuid.NewString()
	return func(o *options) {
		for _, opt := range []Option{
			WithDaprds(2),
			WithStagedOperations(),
			WithDaprdOptions(0, daprd.WithAppID(appID), daprd.WithWorkflowFallbackSettle(t, settle)),
			WithDaprdOptions(1, daprd.WithAppID(appID), daprd.WithWorkflowFallbackSettle(t, settle), daprd.WithWorkflowFallbackDamping(t, damping)),
		} {
			opt(o)
		}
	}
}

// PlacementBack returns a placement service, not yet running, which takes
// over the identity and address of the placement service once that one is
// stopped, so the daprds reconnect to it. Start it with RunPlacementBack. The
// placement service must be pinned with WithPlacementService. Under mTLS it
// takes the same Sentry, so the daprds' TLS clients reconnect.
func (w *Workflow) PlacementBack(t *testing.T) *placement.Placement {
	t.Helper()

	p := w.Placement()
	opts := []placement.Option{
		placement.WithID(p.ID()),
		placement.WithPort(p.Port()),
		placement.WithHealthzPort(p.HealthzPort()),
		placement.WithMetricsPort(p.MetricsPort()),
		placement.WithInitialCluster(p.InitialCluster()),
		placement.WithInitialClusterPorts(p.InitialClusterPorts()...),
	}
	if sen := w.Sentry(); sen != nil {
		opts = append(opts, placement.WithSentry(t, sen))
	}
	return placement.New(t, opts...)
}

// RunPlacementBack runs a placement service from PlacementBack and waits until
// it is healthy. Its cleanup is registered here, so it runs before the test
// case's context is cancelled, which would kill the process.
func RunPlacementBack(t *testing.T, ctx context.Context, back *placement.Placement) {
	t.Helper()

	back.Run(t, ctx)
	t.Cleanup(func() { back.Cleanup(t) })
	back.WaitUntilRunning(t, ctx)
}

// ReminderDeliveries returns how many actor reminders the scheduler delivered
// to the daprd at index, of any actor type and whatever their outcome: the
// daprd counts each job it receives on its scheduler stream for an actor
// reminder, before it routes the call to the actor's owner. A failed metrics
// read fails t.
func (w *Workflow) ReminderDeliveries(t assert.TestingT, ctx context.Context, index int) float64 {
	return w.DaprN(index).Metrics(t, ctx).SumWithLabels("dapr_runtime_actor_reminders_fired_total")
}

// StagedStarts returns how many workflow starts the daprd at index staged as
// a fallback host. A failed metrics read fails t.
func (w *Workflow) StagedStarts(t assert.TestingT, ctx context.Context, index int) float64 {
	return w.DaprN(index).Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_staged_operation_count", "operation:create_workflow")
}

// WaitPending waits, up to timeout for each, until mgmt reads every one of
// ids as PENDING. Each read gets its own short deadline, so a read routed to
// a daprd that is going away is retried rather than blocking the wait.
func WaitPending(t *testing.T, ctx context.Context, mgmt *client.TaskHubGrpcClient, timeout time.Duration, ids ...api.InstanceID) {
	t.Helper()
	for _, id := range ids {
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			fctx, cancel := context.WithTimeout(ctx, time.Second*3)
			defer cancel()
			meta, err := mgmt.FetchWorkflowMetadata(fctx, id)
			if assert.NoError(c, err) {
				assert.Equal(c, protos.OrchestrationStatus_ORCHESTRATION_STATUS_PENDING, meta.GetRuntimeStatus())
			}
		}, timeout, time.Millisecond*100, id)
	}
}
