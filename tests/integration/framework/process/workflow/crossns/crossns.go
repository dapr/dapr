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

// Package crossns builds the control-plane processes and multi-namespace
// daprd set-up needed to integration-test the cross-namespace workflow
// invocation feature. Daprds run in self-hosted mode with the NAMESPACE
// env var splitting them into logically distinct namespaces; mTLS is
// supplied by Sentry so SPIFFE identity is extractable on the target side.
package crossns

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

const (
	defaultCallerNS = "default"
	defaultTargetNS = "other-ns"
	defaultCaller   = "xns-caller"
	defaultTarget   = "xns-target"

	// ShouldNotRun is a sentinel return value for target-side workflow
	// stubs that must not execute in a given test. Asserting on the
	// absence of this string in the caller's output confirms the call
	// was rejected before reaching the child.
	ShouldNotRun = "should-not-run"
)

// Workflow is the cross-namespace integration fixture. Satisfies the
// framework process.Interface contract so it can be passed to
// framework.WithProcesses directly.
type Workflow struct {
	callerNamespace string
	callerAppID     string
	targetNamespace string
	targetAppID     string
	wfaclEnabled    bool

	sen   *sentry.Sentry
	place *placement.Placement
	sched *scheduler.Scheduler

	callers    []*daprd.Daprd
	targets    []*daprd.Daprd
	callerOpts []daprd.Option
	targetOpts []daprd.Option

	procs []process.Interface
}

// New constructs the cross-namespace fixture. Pass the returned *Workflow
// to framework.WithProcesses.
func New(t *testing.T, fopts ...Option) *Workflow {
	t.Helper()

	opts := options{
		callerNS:       defaultCallerNS,
		callerAppID:    defaultCaller,
		targetNS:       defaultTargetNS,
		targetAppID:    defaultTarget,
		callerReplicas: 1,
		targetReplicas: 1,
	}
	for _, fopt := range fopts {
		fopt(&opts)
	}
	require.GreaterOrEqual(t, opts.callerReplicas, 1, "callerReplicas >= 1")
	require.GreaterOrEqual(t, opts.targetReplicas, 1, "targetReplicas >= 1")

	w := &Workflow{
		callerNamespace: opts.callerNS,
		callerAppID:     opts.callerAppID,
		targetNamespace: opts.targetNS,
		targetAppID:     opts.targetAppID,
		wfaclEnabled:    opts.wfaclEnabled,
	}

	w.sen = sentry.New(t)
	w.place = placement.New(t, placement.WithSentry(t, w.sen))
	w.sched = scheduler.New(t,
		scheduler.WithSentry(w.sen),
		scheduler.WithID("dapr-scheduler-server-0"),
	)

	configFile := writeConfig(t, w.wfaclEnabled)
	callerResDir := writeResources(t, opts.policies, opts.callerNS)
	targetResDir := writeResources(t, opts.policies, opts.targetNS)

	commonOpts := []daprd.Option{
		daprd.WithConfigs(configFile),
		daprd.WithSentry(t, w.sen),
		daprd.WithPlacementAddresses(w.place.Address()),
		daprd.WithSchedulerAddresses(w.sched.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
	}

	w.callerOpts = append(append([]daprd.Option(nil), commonOpts...),
		daprd.WithAppID(opts.callerAppID),
		daprd.WithNamespace(opts.callerNS),
		daprd.WithResourcesDir(callerResDir),
	)
	w.targetOpts = append(append([]daprd.Option(nil), commonOpts...),
		daprd.WithAppID(opts.targetAppID),
		daprd.WithNamespace(opts.targetNS),
		daprd.WithResourcesDir(targetResDir),
	)

	w.callers = make([]*daprd.Daprd, opts.callerReplicas)
	for i := range w.callers {
		w.callers[i] = daprd.New(t, w.callerOpts...)
	}
	w.targets = make([]*daprd.Daprd, opts.targetReplicas)
	for i := range w.targets {
		w.targets[i] = daprd.New(t, w.targetOpts...)
	}

	w.procs = []process.Interface{w.sen, w.sched, w.place}
	for _, d := range w.callers {
		w.procs = append(w.procs, d)
	}
	for _, d := range w.targets {
		w.procs = append(w.procs, d)
	}

	return w
}

// writeConfig drops a Configuration file enabling the WorkflowAccessPolicy
// feature flag (when requested) into a temp directory and returns its path.
func writeConfig(t *testing.T, wfaclEnabled bool) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yamlBody := []byte(`apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: daprsystem
spec:
  features:
  - name: WorkflowAccessPolicy
    enabled: ` + boolStr(wfaclEnabled) + `
`)
	require.NoError(t, os.WriteFile(path, yamlBody, 0o600))
	return path
}

// writeResources serializes the WorkflowAccessPolicy resources scoped to
// namespace ns into a per-replica resources directory. The standalone
// loader scopes by appID, not namespace, so we filter to only those
// policies whose ObjectMeta.Namespace matches ns.
func writeResources(t *testing.T, policies []*wfaclapi.WorkflowAccessPolicy, ns string) string {
	t.Helper()
	dir := t.TempDir()
	for i, p := range policies {
		if p == nil {
			continue
		}
		if p.Namespace != "" && p.Namespace != ns {
			continue
		}
		if p.TypeMeta.Kind == "" {
			p.TypeMeta.Kind = "WorkflowAccessPolicy"
		}
		if p.TypeMeta.APIVersion == "" {
			p.TypeMeta.APIVersion = "dapr.io/v1alpha1"
		}
		body, err := yaml.Marshal(p)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "policy-"+itoa(i)+".yaml"), body, 0o600))
	}
	return dir
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [16]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

// Run starts every composed process in dependency order. Implements
// process.Interface.
func (w *Workflow) Run(t *testing.T, ctx context.Context) {
	for _, p := range w.procs {
		p.Run(t, ctx)
	}
}

// Cleanup tears processes down in reverse order. Implements
// process.Interface.
func (w *Workflow) Cleanup(t *testing.T) {
	for i := len(w.procs) - 1; i >= 0; i-- {
		w.procs[i].Cleanup(t)
	}
}

// WaitUntilRunning blocks until control-plane processes and all daprd
// replicas are running.
func (w *Workflow) WaitUntilRunning(t *testing.T, ctx context.Context) {
	t.Helper()
	w.place.WaitUntilRunning(t, ctx)
	w.sched.WaitUntilRunning(t, ctx)
	for _, d := range w.callers {
		d.WaitUntilRunning(t, ctx)
	}
	for _, d := range w.targets {
		d.WaitUntilRunning(t, ctx)
	}
}

// Caller returns the first caller replica.
func (w *Workflow) Caller() *daprd.Daprd { return w.callers[0] }

// Target returns the first target replica.
func (w *Workflow) Target() *daprd.Daprd { return w.targets[0] }

// CallerN returns the i-th caller replica.
func (w *Workflow) CallerN(i int) *daprd.Daprd { return w.callers[i] }

// TargetN returns the i-th target replica.
func (w *Workflow) TargetN(i int) *daprd.Daprd { return w.targets[i] }

// Callers returns every caller replica.
func (w *Workflow) Callers() []*daprd.Daprd { return w.callers }

// Targets returns every target replica.
func (w *Workflow) Targets() []*daprd.Daprd { return w.targets }

// RestartCaller kills the first caller replica and replaces it with a
// freshly-constructed one using the same options. Scheduler reminders are
// durable across restarts so in-flight xns-dispatch reminders re-fire
// after the replacement daprd comes up — this is how tests exercise the
// caller-side retry path.
func (w *Workflow) RestartCaller(t *testing.T, ctx context.Context) {
	t.Helper()
	w.callers[0].Kill(t)
	w.callers[0] = daprd.New(t, w.callerOpts...)
	w.callers[0].Run(t, ctx)
	w.callers[0].WaitUntilRunning(t, ctx)
	t.Cleanup(func() { w.callers[0].Kill(t) })
}

// StartListeners attaches the task registries to every caller and target
// replica (each replica gets its own work-item stream so placement can
// route activations to any replica) and returns the first caller-side
// client, ready to schedule workflows. Blocks until at least one active
// actor is visible on each side so the first ScheduleNewWorkflow doesn't
// race actor registration.
func (w *Workflow) StartListeners(t *testing.T, ctx context.Context, callerReg, targetReg *task.TaskRegistry) *client.TaskHubGrpcClient {
	t.Helper()

	callerClients := make([]*client.TaskHubGrpcClient, len(w.callers))
	for i, d := range w.callers {
		callerClients[i] = client.NewTaskHubGrpcClient(d.GRPCConn(t, ctx), logger.New(t))
		require.NoError(t, callerClients[i].StartWorkItemListener(ctx, callerReg))
	}
	for _, d := range w.targets {
		tc := client.NewTaskHubGrpcClient(d.GRPCConn(t, ctx), logger.New(t))
		require.NoError(t, tc.StartWorkItemListener(ctx, targetReg))
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		callerActors := 0
		for _, d := range w.callers {
			callerActors += len(d.GetMetadata(t, ctx).ActorRuntime.ActiveActors)
		}
		targetActors := 0
		for _, d := range w.targets {
			targetActors += len(d.GetMetadata(t, ctx).ActorRuntime.ActiveActors)
		}
		assert.GreaterOrEqual(c, callerActors, 1)
		assert.GreaterOrEqual(c, targetActors, 1)
	}, time.Second*20, time.Millisecond*10)

	return callerClients[0]
}
