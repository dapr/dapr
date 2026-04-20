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
// invocation feature. It composes the existing framework processes
// (sentry, kubernetes mock, operator, scheduler, placement, daprd) into
// one helper so tests declare only the policies and assertions they care
// about. Uses kubernetes mode with Sentry-issued mTLS because the cross-ns
// feature requires SPIFFE identity to be extractable on the target side.
package crossns

import (
	"context"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/manifest"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes/store"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
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
	trustDomain     = "integration.test.dapr.io"

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
	kube  *kubernetes.Kubernetes
	oper  *operator.Operator
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

	w.sen = sentry.New(t, sentry.WithTrustDomain(trustDomain))

	policyStore := store.New(metav1.GroupVersionKind{
		Group: "dapr.io", Version: "v1alpha1", Kind: "WorkflowAccessPolicy",
	})
	for _, p := range opts.policies {
		if p.TypeMeta.Kind == "" {
			p.TypeMeta = metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "WorkflowAccessPolicy"}
		}
		policyStore.Add(p)
	}

	configForNS := func(ns string) configapi.Configuration {
		return configapi.Configuration{
			TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Configuration"},
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "daprsystem"},
			Spec: configapi.ConfigurationSpec{
				Features: []configapi.FeatureSpec{
					{Name: "WorkflowAccessPolicy", Enabled: &w.wfaclEnabled},
				},
				MTLSSpec: &configapi.MTLSSpec{
					ControlPlaneTrustDomain: trustDomain,
					SentryAddress:           w.sen.Address(),
				},
			},
		}
	}
	configs := []configapi.Configuration{configForNS(opts.callerNS)}
	if opts.targetNS != opts.callerNS {
		configs = append(configs, configForNS(opts.targetNS))
	}

	w.kube = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString(trustDomain),
			"default",
			w.sen.Port(),
		),
		kubernetes.WithClusterDaprConfigurationList(t, &configapi.ConfigurationList{Items: configs}),
		kubernetes.WithClusterDaprComponentList(t, &compapi.ComponentList{
			Items: []compapi.Component{
				manifest.ActorInMemoryStateComponent(opts.callerNS, "mystore"),
				manifest.ActorInMemoryStateComponent(opts.targetNS, "mystore"),
			},
		}),
		kubernetes.WithClusterDaprWorkflowAccessPolicyListFromStore(t, policyStore),
	)

	w.oper = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(w.kube.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(w.sen.TrustAnchorsFile(t)),
	)

	w.place = placement.New(t, placement.WithSentry(t, w.sen))

	w.sched = scheduler.New(t,
		scheduler.WithSentry(w.sen),
		scheduler.WithKubeconfig(w.kube.KubeconfigPath(t)),
		scheduler.WithMode("kubernetes"),
		scheduler.WithID("dapr-scheduler-server-0"),
	)

	commonOpts := []daprd.Option{
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("daprsystem"),
		daprd.WithSentry(t, w.sen),
		daprd.WithControlPlaneAddress(w.oper.Address()),
		daprd.WithPlacementAddresses(w.place.Address()),
		daprd.WithSchedulerAddresses(w.sched.Address()),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithControlPlaneTrustDomain(trustDomain),
	}

	w.callerOpts = append(append([]daprd.Option(nil), commonOpts...),
		daprd.WithAppID(opts.callerAppID),
		daprd.WithNamespace(opts.callerNS),
	)
	w.targetOpts = append(append([]daprd.Option(nil), commonOpts...),
		daprd.WithAppID(opts.targetAppID),
		daprd.WithNamespace(opts.targetNS),
	)

	w.callers = make([]*daprd.Daprd, opts.callerReplicas)
	for i := range w.callers {
		w.callers[i] = daprd.New(t, w.callerOpts...)
	}
	w.targets = make([]*daprd.Daprd, opts.targetReplicas)
	for i := range w.targets {
		w.targets[i] = daprd.New(t, w.targetOpts...)
	}

	w.procs = []process.Interface{w.sen, w.kube, w.oper, w.sched, w.place}
	for _, d := range w.callers {
		w.procs = append(w.procs, d)
	}
	for _, d := range w.targets {
		w.procs = append(w.procs, d)
	}

	return w
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
	w.oper.WaitUntilRunning(t, ctx)
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
