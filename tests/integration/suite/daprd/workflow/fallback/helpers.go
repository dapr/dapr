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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"

	fclient "github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/client"
)

// fallbackSettle is the settle period the tests give their daprds in place
// of the default, so a daprd without a worker turns into a fallback host soon
// after the placement tables show that no daprd of the app has one.
const fallbackSettle = time.Second

// doneOutput is what the tests' workflows return once they complete.
const doneOutput = "done"

// schedulerJobs returns a lookup of the Scheduler job keys of an instance,
// for polls. It dials etcd once, here on the test goroutine, and a poll
// reports a failed lookup to its collector, which retries it.
func schedulerJobs(t *testing.T, ctx context.Context, wf *workflow.Workflow) func(c *assert.CollectT, id api.InstanceID) []string {
	t.Helper()
	etcd := fclient.Etcd(t, clientv3.Config{
		Endpoints:   []string{"127.0.0.1:" + strconv.Itoa(wf.Scheduler().EtcdClientPort())},
		DialTimeout: 5 * time.Second,
		Context:     ctx,
	})
	return func(c *assert.CollectT, id api.InstanceID) []string {
		all, err := etcd.ListAllKeys(ctx, "dapr/jobs")
		if !assert.NoError(c, err) {
			return nil
		}
		var keys []string
		for _, key := range all {
			if strings.Contains(key, string(id)) {
				keys = append(keys, key)
			}
		}
		return keys
	}
}

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
// must pin the placement service with workflow.WithPlacementService. Under
// mTLS it takes the same Sentry, so the daprds' TLS clients reconnect.
func placementBack(t *testing.T, wf *workflow.Workflow) *placement.Placement {
	t.Helper()

	p := wf.Placement()
	opts := []placement.Option{
		placement.WithID(p.ID()),
		placement.WithPort(p.Port()),
		placement.WithHealthzPort(p.HealthzPort()),
		placement.WithMetricsPort(p.MetricsPort()),
		placement.WithInitialCluster(p.InitialCluster()),
		placement.WithInitialClusterPorts(p.InitialClusterPorts()...),
	}
	if sen := wf.Sentry(); sen != nil {
		opts = append(opts, placement.WithSentry(t, sen))
	}
	return placement.New(t, opts...)
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

// stagedStarts returns how many workflow starts the daprd at index staged as
// a fallback host. A failed metrics read fails c.
func stagedStarts(c assert.TestingT, ctx context.Context, wf *workflow.Workflow, index int) float64 {
	return wf.DaprN(index).Metrics(c, ctx).SumWithLabels("dapr_runtime_workflow_staged_operation_count", "operation:create_workflow")
}

// waitPending waits, up to timeout for each, until mgmt reads every one of
// ids as PENDING. Each read gets its own short deadline, so a read routed to
// a daprd that is going away is retried rather than blocking the wait.
func waitPending(t *testing.T, ctx context.Context, mgmt *client.TaskHubGrpcClient, timeout time.Duration, ids ...api.InstanceID) {
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
