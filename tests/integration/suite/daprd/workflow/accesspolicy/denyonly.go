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
	suite.Register(new(denyonly))
}

// denyonly tests that a caller appearing ONLY in deny rules cannot invoke
// any workflow operations on the target. This validates VULN-1 fix:
// IsCallerKnown now requires at least one allow action, so deny-only
// callers are fully rejected for both subject and non-subject methods.
type denyonly struct {
	target      *daprd.Daprd
	denyOnlyApp *daprd.Daprd
	place       *placement.Placement
	sched       *scheduler.Scheduler
	kubeapi     *kubernetes.Kubernetes
	oper        *operator.Operator
}

func (d *denyonly) Setup(t *testing.T) []framework.Option {
	sen := sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	policyStore := store.New(metav1.GroupVersionKind{
		Group: "dapr.io", Version: "v1alpha1", Kind: "WorkflowAccessPolicy",
	})
	policyStore.Add(&wfaclapi.WorkflowAccessPolicy{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "WorkflowAccessPolicy"},
		ObjectMeta: metav1.ObjectMeta{Name: "denyonly-test", Namespace: "default"},
		Scoped:     common.Scoped{Scopes: []string{"denyonly-target"}},
		Spec: wfaclapi.WorkflowAccessPolicySpec{
			DefaultAction: wfaclapi.PolicyActionDeny,
			Rules: []wfaclapi.WorkflowAccessPolicyRule{
				{
					Callers: []wfaclapi.WorkflowCaller{{AppID: "allowed-caller"}},
					Operations: []wfaclapi.WorkflowOperationRule{
						{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "AllowedWF", Action: wfaclapi.PolicyActionAllow},
						{Type: wfaclapi.WorkflowOperationTypeActivity, Name: "*", Action: wfaclapi.PolicyActionAllow},
					},
				},
				{
					Callers: []wfaclapi.WorkflowCaller{{AppID: "denyonly-target"}},
					Operations: []wfaclapi.WorkflowOperationRule{
						{Type: wfaclapi.WorkflowOperationTypeActivity, Name: "*", Action: wfaclapi.PolicyActionAllow},
					},
				},
				{
					Callers: []wfaclapi.WorkflowCaller{{AppID: "denyonly-caller"}},
					Operations: []wfaclapi.WorkflowOperationRule{
						{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionDeny},
					},
				},
			},
		},
	})

	boolTrue := true
	d.kubeapi = kubernetes.New(t,
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

	d.oper = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(d.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(sen.TrustAnchorsFile(t)),
	)

	d.place = placement.New(t, placement.WithSentry(t, sen))
	d.sched = scheduler.New(t,
		scheduler.WithSentry(sen),
		scheduler.WithKubeconfig(d.kubeapi.KubeconfigPath(t)),
		scheduler.WithMode("kubernetes"),
		scheduler.WithID("dapr-scheduler-server-0"),
	)

	commonOpts := []daprd.Option{
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("daprsystem"),
		daprd.WithNamespace("default"),
		daprd.WithSentry(t, sen),
		daprd.WithControlPlaneAddress(d.oper.Address()),
		daprd.WithPlacementAddresses(d.place.Address()),
		daprd.WithSchedulerAddresses(d.sched.Address()),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithControlPlaneTrustDomain("integration.test.dapr.io"),
	}

	d.target = daprd.New(t, append(commonOpts,
		daprd.WithAppID("denyonly-target"),
	)...)

	d.denyOnlyApp = daprd.New(t, append(commonOpts,
		daprd.WithAppID("denyonly-caller"),
	)...)

	return []framework.Option{
		framework.WithProcesses(sen, d.kubeapi, d.oper, d.sched, d.place, d.target, d.denyOnlyApp),
	}
}

func (d *denyonly) Run(t *testing.T, ctx context.Context) {
	d.oper.WaitUntilRunning(t, ctx)
	d.place.WaitUntilRunning(t, ctx)
	d.sched.WaitUntilRunning(t, ctx)
	d.target.WaitUntilRunning(t, ctx)
	d.denyOnlyApp.WaitUntilRunning(t, ctx)

	denyOnlyRegistry := task.NewTaskRegistry()
	require.NoError(t, denyOnlyRegistry.AddWorkflowN("TryScheduleFromDenyOnly", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		err := ctx.CallChildWorkflow("AllowedWF",
			task.WithChildWorkflowAppID(d.target.AppID())).
			Await(&output)
		if err != nil {
			return nil, fmt.Errorf("sub-orchestrator failed: %w", err)
		}
		return output, nil
	}))

	targetRegistry := task.NewTaskRegistry()
	require.NoError(t, targetRegistry.AddWorkflowN("AllowedWF", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	}))

	denyOnlyClient := client.NewTaskHubGrpcClient(d.denyOnlyApp.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, denyOnlyClient.StartWorkItemListener(ctx, denyOnlyRegistry))
	targetClient := client.NewTaskHubGrpcClient(d.target.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, targetClient.StartWorkItemListener(ctx, targetRegistry))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(d.denyOnlyApp.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
		assert.GreaterOrEqual(c, len(d.target.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	t.Run("deny-only caller cannot schedule workflows on target", func(t *testing.T) {
		id, err := denyOnlyClient.ScheduleNewWorkflow(ctx, "TryScheduleFromDenyOnly")
		if err != nil {
			assert.Contains(t, err.Error(), "PermissionDenied")
			return
		}

		metadata, err := denyOnlyClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails(),
			"deny-only caller should not be able to schedule workflows on target")
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(), "denied by workflow access policy")
	})
}
