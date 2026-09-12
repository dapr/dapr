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

package workflow

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
	"github.com/dapr/durabletask-go/workflow"
	kitstrings "github.com/dapr/kit/strings"
)

// ClusteredDeploymentConfig is a Configuration manifest enabling the
// WorkflowsClusteredDeployment preview feature.
const ClusteredDeploymentConfig = `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
    name: workflowsclustereddeployment
spec:
    features:
    - name: WorkflowsClusteredDeployment
      enabled: true
`

// ClusteredDeploymentFromEnv reports whether the suite is running with
// DAPR_INTEGRATION_WORKFLOW_CLUSTERED set truthy, which enables the
// WorkflowsClusteredDeployment feature flag on every daprd built by this
// harness unless a test overrides it with WithClusteredDeployment.
func ClusteredDeploymentFromEnv() bool {
	return kitstrings.IsTruthy(os.Getenv("DAPR_INTEGRATION_WORKFLOW_CLUSTERED"))
}

// FastPathFromEnv reports whether the suite is running with
// DAPR_INTEGRATION_WORKFLOW_FASTPATH set truthy, which enables the
// WorkflowsFastPath feature flag on every daprd built by this harness unless
// a test overrides it with WithFastPath.
func FastPathFromEnv() bool {
	return kitstrings.IsTruthy(os.Getenv("DAPR_INTEGRATION_WORKFLOW_FASTPATH"))
}

type Workflow struct {
	taskregistry []*task.TaskRegistry
	db           *sqlite.SQLite
	place        *placement.Placement
	sched        *scheduler.Scheduler
	ownsSched    bool
	sentry       *sentry.Sentry
	daprds       []*daprd.Daprd
	clustered    bool
	fastPath     bool
}

func New(t *testing.T, fopts ...Option) *Workflow {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on Windows due to SQLite limitations")
	}

	opts := options{
		daprds: 1,
	}
	for _, fopt := range fopts {
		fopt(&opts)
	}

	require.GreaterOrEqual(t, opts.daprds, 1, "at least one daprd instance is required")

	clustered := ClusteredDeploymentFromEnv()
	if opts.clustered != nil {
		clustered = *opts.clustered
	}

	fastPath := FastPathFromEnv()
	if opts.fastPath != nil {
		fastPath = *opts.fastPath
	}

	db := sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)

	var sen *sentry.Sentry
	var placementOpts []placement.Option
	placementOpts = append(placementOpts, opts.placementOptions...)
	var schedulerOpts []scheduler.Option
	schedulerOpts = append(schedulerOpts, opts.schedulerOptions...)
	if opts.mtls {
		sen = sentry.New(t)
		placementOpts = append(placementOpts, placement.WithSentry(t, sen))
		// Scheduler ID must match the TLS cert DNS names issued by Sentry.
		schedulerOpts = append(schedulerOpts,
			scheduler.WithSentry(sen),
			scheduler.WithID("dapr-scheduler-server-0"),
		)
	}

	place := placement.New(t, placementOpts...)
	sched := opts.schedulerInstance
	ownsSched := false
	if sched == nil {
		sched = scheduler.New(t, schedulerOpts...)
		ownsSched = true
	}

	baseDopts := []daprd.Option{
		daprd.WithPlacementAddresses(place.Address()),
	}

	if !opts.skipDB {
		baseDopts = append(baseDopts, daprd.WithResourceFiles(db.GetComponent(t)))
	}

	if sen != nil {
		baseDopts = append(baseDopts, daprd.WithSentry(t, sen))
	}

	// daprd loads each config file onto one Configuration struct and
	// spec.features is a slice, so the LAST config file that specifies
	// features replaces every earlier feature list. All harness-driven
	// features must therefore land in a single manifest per daprd; it is
	// built in the per-daprd loop below because signing is per-daprd.
	baseFeatures := baseFeatureList(clustered, fastPath)

	if opts.schedulerAddress != nil {
		// Reset so a caller-supplied override (e.g. a proxy in front of the
		// scheduler) truly replaces any addresses appended by other option
		// layers, instead of being one entry among many.
		baseDopts = append(baseDopts, daprd.WithSchedulerAddressesReset(*opts.schedulerAddress))
	} else {
		baseDopts = append(baseDopts, daprd.WithScheduler(sched))
	}

	signingDisabled := make(map[int]bool, len(opts.signingDisabled))
	for _, idx := range opts.signingDisabled {
		signingDisabled[idx] = true
	}

	daprds := make([]*daprd.Daprd, opts.daprds)

	for i := range daprds {
		dopts := make([]daprd.Option, 0, len(baseDopts)+1)
		dopts = append(dopts, baseDopts...)

		features := baseFeatures
		if sen != nil && (opts.signing || opts.mtls) && !signingDisabled[i] {
			features = append(features[:len(features):len(features)], "WorkflowHistorySigning")
		}
		if len(features) > 0 {
			dopts = append(dopts, daprd.WithFeatureEnabled(t, features...))
		}

		// Add specific opts for this daprd
		for _, daprdOpt := range opts.daprdOptions {
			if daprdOpt.index == i {
				dopts = append(dopts, daprdOpt.opts...)
			}
		}

		daprds[i] = daprd.New(t, dopts...)
	}

	registries := make(map[int]*task.TaskRegistry)
	for i := range daprds {
		registries[i] = task.NewTaskRegistry()
	}

	// Apply orchestrators & activities to the registry
	for _, orch := range opts.orchestrators {
		if orch.index < len(daprds) {
			require.NoError(t, registries[orch.index].AddWorkflowN(orch.name, orch.fn))
		}
	}
	for _, act := range opts.activities {
		if act.index < len(daprds) {
			require.NoError(t, registries[act.index].AddActivityN(act.name, act.fn))
		}
	}

	workflow := &Workflow{
		taskregistry: make([]*task.TaskRegistry, len(daprds)),
		db:           db,
		place:        place,
		sched:        sched,
		ownsSched:    ownsSched,
		sentry:       sen,
		daprds:       daprds,
		clustered:    clustered,
		fastPath:     fastPath,
	}

	for i := range workflow.taskregistry {
		workflow.taskregistry[i] = registries[i]
	}

	return workflow
}

func (w *Workflow) Run(t *testing.T, ctx context.Context) {
	w.db.Run(t, ctx)
	if w.sentry != nil {
		w.sentry.Run(t, ctx)
	}
	w.place.Run(t, ctx)
	if w.ownsSched {
		w.sched.Run(t, ctx)
	}
	for _, daprd := range w.daprds {
		daprd.Run(t, ctx)
	}
}

func (w *Workflow) Cleanup(t *testing.T) {
	for _, daprd := range w.daprds {
		daprd.Cleanup(t)
	}
	if w.ownsSched {
		w.sched.Cleanup(t)
	}
	w.place.Cleanup(t)
	if w.sentry != nil {
		w.sentry.Cleanup(t)
	}
	w.db.Cleanup(t)
}

func (w *Workflow) WaitUntilRunning(t *testing.T, ctx context.Context) {
	w.place.WaitUntilRunning(t, ctx)
	if w.sched != nil {
		w.sched.WaitUntilRunning(t, ctx)
	}
	for _, daprd := range w.daprds {
		daprd.WaitUntilRunning(t, ctx)
	}
}

func (w *Workflow) WaitForNoConnectedWorkers(t *testing.T, ctx context.Context) {
	t.Helper()
	w.WaitForNoConnectedWorkersN(t, ctx, 0)
}

func (w *Workflow) WaitForNoConnectedWorkersN(t *testing.T, ctx context.Context, index int) {
	t.Helper()
	require.Less(t, index, len(w.daprds), "index out of range")

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		md := w.DaprN(index).GetMetadata(c, ctx)
		if !assert.NotNil(c, md) {
			return
		}
		// The workflows metadata field is omitted when there are no connected
		// workers, so a nil value means zero workers (drained).
		if md.Workflows == nil {
			return
		}
		assert.Zero(c, md.Workflows.ConnectedWorkers)
	}, time.Second*30, time.Millisecond*10)
}

func (w *Workflow) ResetRegistry(t *testing.T) {
	t.Helper()
	w.taskregistry[0] = task.NewTaskRegistry()
}

func (w *Workflow) Registry() *task.TaskRegistry {
	return w.taskregistry[0]
}

// Registry returns the registry for a specific index
func (w *Workflow) RegistryN(index int) *task.TaskRegistry {
	return w.taskregistry[index]
}

func (w *Workflow) WorkflowClient(t *testing.T, ctx context.Context) *workflow.Client {
	t.Helper()
	return workflow.NewClient(w.Dapr().GRPCConn(t, ctx))
}

func (w *Workflow) WorkflowClientN(t *testing.T, ctx context.Context, index int) *workflow.Client {
	t.Helper()
	require.Less(t, index, len(w.daprds), "index out of range")
	return workflow.NewClient(w.DaprN(index).GRPCConn(t, ctx))
}

func (w *Workflow) BackendClient(t *testing.T, ctx context.Context) *client.TaskHubGrpcClient {
	t.Helper()

	return w.BackendClientN(t, ctx, 0)
}

// BackendClient returns a backend client for the specified index
func (w *Workflow) BackendClientN(t *testing.T, ctx context.Context, index int) *client.TaskHubGrpcClient {
	t.Helper()
	require.Less(t, index, len(w.daprds), "index out of range")

	backendClient := client.NewTaskHubGrpcClient(w.daprds[index].GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, backendClient.StartWorkItemListener(ctx, w.RegistryN(index)))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		// GetMetadata can return a partially-populated value if the underlying
		// HTTP request fails or times out (the request runs under a derived
		// context with its own deadline). Guard each field access so a nil
		// here causes the Eventually tick to retry rather than panic the
		// whole test binary.
		md := w.DaprN(index).GetMetadata(t, ctx)
		if !assert.NotNil(c, md) {
			return
		}
		if !assert.NotNil(c, md.ActorRuntime) {
			return
		}
		assert.GreaterOrEqual(c, len(md.ActorRuntime.ActiveActors), 3)
		if assert.NotNil(c, md.Workflows) {
			assert.GreaterOrEqual(c, md.Workflows.ConnectedWorkers, 1)
		}
	}, time.Second*60, time.Millisecond*10)

	return backendClient
}

func (w *Workflow) GRPCClient(t *testing.T, ctx context.Context) rtv1.DaprClient {
	t.Helper()
	return w.daprds[0].GRPCClient(t, ctx)
}

// GRPCClientForApp returns a GRPC client for the specified app index
func (w *Workflow) GRPCClientN(t *testing.T, ctx context.Context, index int) rtv1.DaprClient {
	t.Helper()
	require.Less(t, index, len(w.daprds), "index out of range")
	return w.daprds[index].GRPCClient(t, ctx)
}

func baseFeatureList(clustered, fastPath bool) []string {
	features := make([]string, 0, 2)
	if clustered {
		features = append(features, "WorkflowsClusteredDeployment")
	}
	if fastPath {
		features = append(features, "WorkflowsFastPath")
	}
	return features
}

// FeatureOptions returns the feature manifest option for extra daprds a test
// adds to this harness's cluster. Only cluster-wide features are covered:
// WorkflowHistorySigning is per-daprd and needs the harness's sentry wiring
// besides the flag. daprd's config merge makes the last spec.features list
// win, so all features must land in one manifest.
func (w *Workflow) FeatureOptions(t *testing.T) []daprd.Option {
	features := baseFeatureList(w.clustered, w.fastPath)
	if len(features) == 0 {
		return nil
	}
	return []daprd.Option{daprd.WithFeatureEnabled(t, features...)}
}

// ClusteredDeployment reports whether every daprd in this workflow runs with
// the WorkflowsClusteredDeployment feature flag enabled. Tests use this to
// branch assertions which differ between the two modes.
func (w *Workflow) ClusteredDeployment() bool {
	return w.clustered
}

// FastPath reports whether every daprd in this workflow runs with the
// WorkflowsFastPath feature flag enabled. Tests use this to branch
// assertions which differ between the two modes.
func (w *Workflow) FastPath() bool {
	return w.fastPath
}

// ActorTypesCount returns the number of actor types a daprd in this workflow
// registers when a worker is connected: workflow, activity and retentioner,
// plus the executor rendezvous type in clustered deployment mode.
func (w *Workflow) ActorTypesCount() int {
	if w.clustered {
		return 4
	}
	return 3
}

func (w *Workflow) Dapr() *daprd.Daprd {
	return w.daprds[0]
}

func (w *Workflow) DaprN(i int) *daprd.Daprd {
	return w.daprds[i]
}

func (w *Workflow) Metrics(t *testing.T, ctx context.Context) map[string]float64 {
	t.Helper()
	return w.daprds[0].Metrics(t, ctx).All()
}

func (w *Workflow) DB() *sqlite.SQLite {
	return w.db
}

func (w *Workflow) Scheduler() *scheduler.Scheduler {
	return w.sched
}

func (w *Workflow) Sentry() *sentry.Sentry {
	return w.sentry
}

func (w *Workflow) Placement() *placement.Placement {
	return w.place
}
