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
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/manifest"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes/store"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(reminderbypass))
}

// reminderbypass tests that an unauthorized sidecar cannot bypass workflow
// access policies. An attacker app (not in any policy rule) attempts to
// schedule cross-app workflows and activities on a target app, and all
// attempts are denied.
type reminderbypass struct {
	target   *daprd.Daprd
	attacker *daprd.Daprd
	place    *placement.Placement
	sched    *scheduler.Scheduler
	kubeapi  *kubernetes.Kubernetes
	oper     *operator.Operator
}

func (r *reminderbypass) Setup(t *testing.T) []framework.Option {
	sen := sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	policyStore := store.New(metav1.GroupVersionKind{
		Group: "dapr.io", Version: "v1alpha1", Kind: "WorkflowAccessPolicy",
	})
	// Policy only allows "legit-caller". "attacker-app" is NOT in any rule.
	policyStore.Add(&wfaclapi.WorkflowAccessPolicy{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "WorkflowAccessPolicy"},
		ObjectMeta: metav1.ObjectMeta{Name: "bypass-test", Namespace: "default"},
		Scoped:     common.Scoped{},
		Spec: wfaclapi.WorkflowAccessPolicySpec{
			DefaultAction: wfaclapi.PolicyActionDeny,
			Rules: []wfaclapi.WorkflowAccessPolicyRule{
				{
					Callers: []wfaclapi.WorkflowCaller{{AppID: "legit-caller"}},
					Operations: []wfaclapi.WorkflowOperationRule{
						{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow},
						{Type: wfaclapi.WorkflowOperationTypeActivity, Name: "*", Action: wfaclapi.PolicyActionAllow},
					},
				},
				{
					// Target must be able to execute its own activities.
					Callers: []wfaclapi.WorkflowCaller{{AppID: "bypass-target"}},
					Operations: []wfaclapi.WorkflowOperationRule{
						{Type: wfaclapi.WorkflowOperationTypeActivity, Name: "*", Action: wfaclapi.PolicyActionAllow},
					},
				},
			},
		},
	})

	boolTrue := true
	r.kubeapi = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			sen.Port(),
		),
		kubernetes.WithClusterDaprConfigurationList(t, &configapi.ConfigurationList{
			Items: []configapi.Configuration{{
				TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Configuration"},
				ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "daprsystem"},
				Spec: configapi.ConfigurationSpec{
					Features: []configapi.FeatureSpec{
						{Name: "WorkflowAccessPolicy", Enabled: &boolTrue},
					},
					MTLSSpec: &configapi.MTLSSpec{
						ControlPlaneTrustDomain: "integration.test.dapr.io",
						SentryAddress:           sen.Address(),
					},
				},
			}},
		}),
		kubernetes.WithClusterDaprComponentList(t, &compapi.ComponentList{
			Items: []compapi.Component{manifest.ActorInMemoryStateComponent("default", "mystore")},
		}),
		kubernetes.WithClusterDaprWorkflowAccessPolicyListFromStore(t, policyStore),
	)

	r.oper = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(r.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(sen.TrustAnchorsFile(t)),
	)

	r.place = placement.New(t, placement.WithSentry(t, sen))
	r.sched = scheduler.New(t,
		scheduler.WithSentry(sen),
		scheduler.WithKubeconfig(r.kubeapi.KubeconfigPath(t)),
		scheduler.WithMode("kubernetes"),
		scheduler.WithID("dapr-scheduler-server-0"),
	)

	commonOpts := []daprd.Option{
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("daprsystem"),
		daprd.WithNamespace("default"),
		daprd.WithSentry(t, sen),
		daprd.WithControlPlaneAddress(r.oper.Address()),
		daprd.WithPlacementAddresses(r.place.Address()),
		daprd.WithSchedulerAddresses(r.sched.Address()),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithControlPlaneTrustDomain("integration.test.dapr.io"),
	}

	r.target = daprd.New(t, append(commonOpts,
		daprd.WithAppID("bypass-target"),
	)...)

	r.attacker = daprd.New(t, append(commonOpts,
		daprd.WithAppID("attacker-app"),
	)...)

	return []framework.Option{
		framework.WithProcesses(sen, r.kubeapi, r.oper, r.sched, r.place, r.target, r.attacker),
	}
}

func (r *reminderbypass) Run(t *testing.T, ctx context.Context) {
	r.oper.WaitUntilRunning(t, ctx)
	r.place.WaitUntilRunning(t, ctx)
	r.sched.WaitUntilRunning(t, ctx)
	r.target.WaitUntilRunning(t, ctx)
	r.attacker.WaitUntilRunning(t, ctx)

	// Set up orchestrator on attacker that tries to call workflows on target.
	registry := task.NewTaskRegistry()
	require.NoError(t, registry.AddWorkflowN("AttackWorkflow", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		err := ctx.CallChildWorkflow("VictimWorkflow",
			task.WithChildWorkflowAppID(r.target.AppID())).
			Await(&output)
		if err != nil {
			return nil, fmt.Errorf("attack failed: %w", err)
		}
		return output, nil
	}))

	require.NoError(t, registry.AddWorkflowN("AttackActivity", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		err := ctx.CallActivity("VictimActivity",
			task.WithActivityAppID(r.target.AppID())).
			Await(&output)
		if err != nil {
			return nil, fmt.Errorf("attack failed: %w", err)
		}
		return output, nil
	}))

	// Register victim workflows/activities on the target.
	targetRegistry := task.NewTaskRegistry()
	require.NoError(t, targetRegistry.AddWorkflowN("VictimWorkflow", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	}))
	require.NoError(t, targetRegistry.AddActivityN("VictimActivity", func(ctx task.ActivityContext) (any, error) {
		return nil, nil
	}))

	attackerClient := client.NewTaskHubGrpcClient(r.attacker.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, attackerClient.StartWorkItemListener(ctx, registry))
	targetClient := client.NewTaskHubGrpcClient(r.target.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, targetClient.StartWorkItemListener(ctx, targetRegistry))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(r.attacker.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
		assert.GreaterOrEqual(c, len(r.target.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	t.Run("attacker cannot schedule cross-app workflow on target", func(t *testing.T) {
		// The attacker tries to schedule an orchestrator that calls the target.
		// The policy denies the attacker from even creating workflows (since
		// attacker-app is not in the callers list for any rule). The denial
		// may happen at ScheduleNewWorkflow or WaitForWorkflowCompletion.
		id, err := attackerClient.ScheduleNewWorkflow(ctx, "AttackWorkflow")
		if err != nil {
			// Denied at scheduling — the local policy check blocks the attacker.
			assert.Contains(t, err.Error(), "PermissionDenied")
			return
		}

		metadata, err := attackerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails(),
			"attacker should not be able to schedule workflows on target")
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(), "denied by workflow access policy")
	})

	t.Run("attacker cannot call cross-app activity on target", func(t *testing.T) {
		id, err := attackerClient.ScheduleNewWorkflow(ctx, "AttackActivity")
		if err != nil {
			assert.Contains(t, err.Error(), "PermissionDenied")
			return
		}

		metadata, err := attackerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails(),
			"attacker should not be able to call activities on target")
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(), "denied by workflow access policy")
	})
}
