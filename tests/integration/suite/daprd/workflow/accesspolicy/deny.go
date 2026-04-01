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
	suite.Register(new(deny))
}

// deny tests that cross-app workflow calls are rejected when the
// WorkflowAccessPolicy explicitly denies or does not mention the caller/operation.
type deny struct {
	daprd0   *daprd.Daprd
	daprd1   *daprd.Daprd
	place    *placement.Placement
	sched    *scheduler.Scheduler
	kubeapi  *kubernetes.Kubernetes
	operator *operator.Operator
}

func (d *deny) Setup(t *testing.T) []framework.Option {
	sen := sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	policyStore := store.New(metav1.GroupVersionKind{
		Group: "dapr.io", Version: "v1alpha1", Kind: "WorkflowAccessPolicy",
	})
	policyStore.Add(&wfaclapi.WorkflowAccessPolicy{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "WorkflowAccessPolicy"},
		ObjectMeta: metav1.ObjectMeta{Name: "deny-test", Namespace: "default"},
		Scoped:     common.Scoped{Scopes: []string{"wfacl-target"}},
		Spec: wfaclapi.WorkflowAccessPolicySpec{
			DefaultAction: wfaclapi.PolicyActionDeny,
			Rules: []wfaclapi.WorkflowAccessPolicyRule{{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "wfacl-caller"}},
				Operations: []wfaclapi.WorkflowOperationRule{
					{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "AllowedWF", Action: wfaclapi.PolicyActionAllow},
					{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "DeniedWF", Action: wfaclapi.PolicyActionDeny},
				},
			}},
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

	d.operator = operator.New(t,
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
		daprd.WithControlPlaneAddress(d.operator.Address()),
		daprd.WithPlacementAddresses(d.place.Address()),
		daprd.WithSchedulerAddresses(d.sched.Address()),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithControlPlaneTrustDomain("integration.test.dapr.io"),
	}

	d.daprd0 = daprd.New(t, append(commonOpts,
		daprd.WithAppID("wfacl-caller"),
	)...)

	d.daprd1 = daprd.New(t, append(commonOpts,
		daprd.WithAppID("wfacl-target"),
	)...)

	return []framework.Option{
		framework.WithProcesses(sen, d.kubeapi, d.operator, d.sched, d.place, d.daprd0, d.daprd1),
	}
}

func (d *deny) Run(t *testing.T, ctx context.Context) {
	d.operator.WaitUntilRunning(t, ctx)
	d.place.WaitUntilRunning(t, ctx)
	d.sched.WaitUntilRunning(t, ctx)
	d.daprd0.WaitUntilRunning(t, ctx)
	d.daprd1.WaitUntilRunning(t, ctx)

	registry0 := task.NewTaskRegistry()
	registry1 := task.NewTaskRegistry()

	targetAppID := d.daprd1.AppID()

	require.NoError(t, registry0.AddWorkflowN("TestDeniedWF", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		err := ctx.CallChildWorkflow("DeniedWF",
			task.WithChildWorkflowAppID(targetAppID)).
			Await(&output)
		if err != nil {
			return nil, fmt.Errorf("sub-orchestrator failed: %w", err)
		}
		return output, nil
	}))

	require.NoError(t, registry0.AddWorkflowN("TestUnmentionedWF", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		err := ctx.CallChildWorkflow("UnmentionedWF",
			task.WithChildWorkflowAppID(targetAppID)).
			Await(&output)
		if err != nil {
			return nil, fmt.Errorf("sub-orchestrator failed: %w", err)
		}
		return output, nil
	}))

	require.NoError(t, registry1.AddWorkflowN("DeniedWF", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	}))

	require.NoError(t, registry1.AddWorkflowN("UnmentionedWF", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	}))

	client0 := client.NewTaskHubGrpcClient(d.daprd0.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, client0.StartWorkItemListener(ctx, registry0))
	client1 := client.NewTaskHubGrpcClient(d.daprd1.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, client1.StartWorkItemListener(ctx, registry1))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(d.daprd0.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
		assert.GreaterOrEqual(c, len(d.daprd1.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	t.Run("explicitly denied workflow fails with error", func(t *testing.T) {
		id, err := client0.ScheduleNewWorkflow(ctx, "TestDeniedWF")
		require.NoError(t, err)

		metadata, err := client0.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails(),
			"orchestration should fail because the sub-orchestrator call is denied")
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(), "denied by workflow access policy")
	})

	t.Run("unmentioned workflow fails with default deny", func(t *testing.T) {
		id, err := client0.ScheduleNewWorkflow(ctx, "TestUnmentionedWF")
		require.NoError(t, err)

		metadata, err := client0.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails(),
			"orchestration should fail because no rule matches (default deny)")
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(), "denied by workflow access policy")
	})
}
