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
	dtclient "github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(remoteenforce))
}

// remoteenforce specifically tests that the CALLEE-SIDE enforcement path
// works — the target's CallActor gRPC handler checks the caller's SPIFFE
// identity and denies unauthorized callers.
//
// Setup: the policy is scoped ONLY to the target ("remote-target"). The
// caller ("remote-caller") has NO policies loaded (nil CompiledPolicies).
// This ensures the local router ACL check on the caller always passes
// (nil = allow all). Any denial MUST come from the remote CallActor
// handler on the target, proving the SPIFFE-based mTLS enforcement.
type remoteenforce struct {
	caller  *daprd.Daprd
	target  *daprd.Daprd
	place   *placement.Placement
	sched   *scheduler.Scheduler
	kubeapi *kubernetes.Kubernetes
	oper    *operator.Operator
}

func (r *remoteenforce) Setup(t *testing.T) []framework.Option {
	sen := sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	policyStore := store.New(metav1.GroupVersionKind{
		Group: "dapr.io", Version: "v1alpha1", Kind: "WorkflowAccessPolicy",
	})
	policyStore.Add(&wfaclapi.WorkflowAccessPolicy{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "WorkflowAccessPolicy"},
		ObjectMeta: metav1.ObjectMeta{Name: "remote-test", Namespace: "default"},
		Scoped:     common.Scoped{Scopes: []string{"remote-target"}},
		Spec: wfaclapi.WorkflowAccessPolicySpec{
			DefaultAction: wfaclapi.PolicyActionDeny,
			Rules: []wfaclapi.WorkflowAccessPolicyRule{
				{
					Callers: []wfaclapi.WorkflowCaller{{AppID: "allowed-caller"}},
					Operations: []wfaclapi.WorkflowOperationRule{
						{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow},
					},
				},
				{
					Callers: []wfaclapi.WorkflowCaller{{AppID: "remote-target"}},
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

	r.caller = daprd.New(t, append(commonOpts,
		daprd.WithAppID("remote-caller"),
	)...)

	r.target = daprd.New(t, append(commonOpts,
		daprd.WithAppID("remote-target"),
	)...)

	return []framework.Option{
		framework.WithProcesses(sen, r.kubeapi, r.oper, r.sched, r.place, r.caller, r.target),
	}
}

func (r *remoteenforce) Run(t *testing.T, ctx context.Context) {
	r.oper.WaitUntilRunning(t, ctx)
	r.place.WaitUntilRunning(t, ctx)
	r.sched.WaitUntilRunning(t, ctx)
	r.caller.WaitUntilRunning(t, ctx)
	r.target.WaitUntilRunning(t, ctx)

	callerRegistry := task.NewTaskRegistry()
	targetRegistry := task.NewTaskRegistry()

	require.NoError(t, callerRegistry.AddWorkflowN("CrossAppCall", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		err := ctx.CallChildWorkflow("TargetWF",
			task.WithChildWorkflowAppID(r.target.AppID())).
			Await(&output)
		if err != nil {
			return nil, fmt.Errorf("cross-app call failed: %w", err)
		}
		return output, nil
	}))

	require.NoError(t, targetRegistry.AddWorkflowN("TargetWF", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	}))

	callerClient := dtclient.NewTaskHubGrpcClient(r.caller.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, callerClient.StartWorkItemListener(ctx, callerRegistry))
	targetClient := dtclient.NewTaskHubGrpcClient(r.target.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, targetClient.StartWorkItemListener(ctx, targetRegistry))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(r.caller.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
		assert.GreaterOrEqual(c, len(r.target.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	t.Run("callee denies unauthorized caller via SPIFFE identity", func(t *testing.T) {
		id, err := callerClient.ScheduleNewWorkflow(ctx, "CrossAppCall")
		require.NoError(t, err)

		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails(),
			"target should deny remote-caller because it's not in the policy's callers list — "+
				"this proves the callee-side SPIFFE-based enforcement is working")
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(),
			"denied by workflow access policy")
	})
}
