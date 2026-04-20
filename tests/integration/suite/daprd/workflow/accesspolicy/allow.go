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
	suite.Register(new(allow))
}

// allow tests that cross-app workflow and activity calls succeed when the
// WorkflowAccessPolicy explicitly allows the caller. Uses mTLS because
// policy enforcement requires SPIFFE identity extraction.
type allow struct {
	daprd0   *daprd.Daprd
	daprd1   *daprd.Daprd
	place    *placement.Placement
	sched    *scheduler.Scheduler
	kubeapi  *kubernetes.Kubernetes
	operator *operator.Operator
}

func (a *allow) Setup(t *testing.T) []framework.Option {
	sen := sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	policyStore := store.New(metav1.GroupVersionKind{
		Group: "dapr.io", Version: "v1alpha1", Kind: "WorkflowAccessPolicy",
	})
	policyStore.Add(&wfaclapi.WorkflowAccessPolicy{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "WorkflowAccessPolicy"},
		ObjectMeta: metav1.ObjectMeta{Name: "allow-test", Namespace: "default"},
		Scoped:     common.Scoped{Scopes: []string{"wfacl-target"}},
		Spec: wfaclapi.WorkflowAccessPolicySpec{
			DefaultAction: wfaclapi.PolicyActionDeny,
			Rules: []wfaclapi.WorkflowAccessPolicyRule{
				{
					Callers: []wfaclapi.WorkflowCaller{{AppID: "wfacl-caller"}},
					Operations: []wfaclapi.WorkflowOperationRule{
						{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "AllowedWorkflow", Action: wfaclapi.PolicyActionAllow},
						{Type: wfaclapi.WorkflowOperationTypeActivity, Name: "AllowedActivity", Action: wfaclapi.PolicyActionAllow},
					},
				},
				{
					Callers: []wfaclapi.WorkflowCaller{{AppID: "wfacl-target"}},
					Operations: []wfaclapi.WorkflowOperationRule{
						{Type: wfaclapi.WorkflowOperationTypeActivity, Name: "*", Action: wfaclapi.PolicyActionAllow},
					},
				},
			},
		},
	})

	boolTrue := true
	a.kubeapi = kubernetes.New(t,
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

	a.operator = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(a.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(sen.TrustAnchorsFile(t)),
	)

	a.place = placement.New(t, placement.WithSentry(t, sen))

	a.sched = scheduler.New(t,
		scheduler.WithSentry(sen),
		scheduler.WithKubeconfig(a.kubeapi.KubeconfigPath(t)),
		scheduler.WithMode("kubernetes"),
		scheduler.WithID("dapr-scheduler-server-0"),
	)

	commonOpts := []daprd.Option{
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("daprsystem"),
		daprd.WithNamespace("default"),
		daprd.WithSentry(t, sen),
		daprd.WithControlPlaneAddress(a.operator.Address()),
		daprd.WithPlacementAddresses(a.place.Address()),
		daprd.WithSchedulerAddresses(a.sched.Address()),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithControlPlaneTrustDomain("integration.test.dapr.io"),
	}

	a.daprd0 = daprd.New(t, append(commonOpts,
		daprd.WithAppID("wfacl-caller"),
	)...)

	a.daprd1 = daprd.New(t, append(commonOpts,
		daprd.WithAppID("wfacl-target"),
	)...)

	return []framework.Option{
		framework.WithProcesses(sen, a.kubeapi, a.operator, a.sched, a.place, a.daprd0, a.daprd1),
	}
}

func (a *allow) Run(t *testing.T, ctx context.Context) {
	a.operator.WaitUntilRunning(t, ctx)
	a.place.WaitUntilRunning(t, ctx)
	a.sched.WaitUntilRunning(t, ctx)
	a.daprd0.WaitUntilRunning(t, ctx)
	a.daprd1.WaitUntilRunning(t, ctx)

	registry0 := task.NewTaskRegistry()
	registry1 := task.NewTaskRegistry()

	require.NoError(t, registry0.AddWorkflowN("AllowedWorkflow", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		err := ctx.CallActivity("AllowedActivity",
			task.WithActivityAppID(a.daprd1.AppID())).
			Await(&output)
		if err != nil {
			return nil, fmt.Errorf("activity failed: %w", err)
		}
		return output, nil
	}))

	require.NoError(t, registry1.AddActivityN("AllowedActivity", func(ctx task.ActivityContext) (any, error) {
		return "allowed-result", nil
	}))

	client0 := client.NewTaskHubGrpcClient(a.daprd0.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, client0.StartWorkItemListener(ctx, registry0))
	client1 := client.NewTaskHubGrpcClient(a.daprd1.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, client1.StartWorkItemListener(ctx, registry1))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(a.daprd0.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
		assert.GreaterOrEqual(c, len(a.daprd1.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	t.Run("allowed cross-app workflow with activity succeeds", func(t *testing.T) {
		id, err := client0.ScheduleNewWorkflow(ctx, "AllowedWorkflow")
		require.NoError(t, err)

		metadata, err := client0.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(metadata))
		assert.Equal(t, `"allowed-result"`, metadata.GetOutput().GetValue())
	})
}
