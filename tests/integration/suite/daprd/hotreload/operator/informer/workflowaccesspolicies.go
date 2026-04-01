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

package informer

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
	suite.Register(new(workflowaccesspolicies))
}

// workflowaccesspolicies tests operator hot-reloading of WorkflowAccessPolicy
// resources using the Kubernetes informer. Uses two daprds (caller and target)
// with mTLS to exercise cross-app enforcement through the operator.
type workflowaccesspolicies struct {
	caller  *daprd.Daprd
	target  *daprd.Daprd
	pStore  *store.Store
	kubeapi *kubernetes.Kubernetes
	oper    *operator.Operator
	place   *placement.Placement
	sched   *scheduler.Scheduler
}

func (w *workflowaccesspolicies) Setup(t *testing.T) []framework.Option {
	sen := sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	w.pStore = store.New(metav1.GroupVersionKind{
		Group:   "dapr.io",
		Version: "v1alpha1",
		Kind:    "WorkflowAccessPolicy",
	})

	boolTrue := true
	w.kubeapi = kubernetes.New(t,
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
					MTLSSpec: &configapi.MTLSSpec{
						ControlPlaneTrustDomain: "integration.test.dapr.io",
						SentryAddress:           sen.Address(),
					},
					Features: []configapi.FeatureSpec{
						{Name: "HotReload", Enabled: &boolTrue},
						{Name: "WorkflowAccessPolicy", Enabled: &boolTrue},
					},
				},
			}},
		}),
		kubernetes.WithClusterDaprComponentList(t, &compapi.ComponentList{
			Items: []compapi.Component{manifest.ActorInMemoryStateComponent("default", "mystore")},
		}),
		kubernetes.WithClusterDaprWorkflowAccessPolicyListFromStore(t, w.pStore),
	)

	w.oper = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(w.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(sen.TrustAnchorsFile(t)),
	)

	w.place = placement.New(t, placement.WithSentry(t, sen))

	w.sched = scheduler.New(t,
		scheduler.WithSentry(sen),
		scheduler.WithKubeconfig(w.kubeapi.KubeconfigPath(t)),
		scheduler.WithMode("kubernetes"),
		scheduler.WithID("dapr-scheduler-server-0"),
	)

	commonOpts := []daprd.Option{
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("daprsystem"),
		daprd.WithNamespace("default"),
		daprd.WithSentry(t, sen),
		daprd.WithControlPlaneAddress(w.oper.Address()),
		daprd.WithPlacementAddresses(w.place.Address()),
		daprd.WithSchedulerAddresses(w.sched.Address()),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithControlPlaneTrustDomain("integration.test.dapr.io"),
	}

	w.caller = daprd.New(t, append(commonOpts,
		daprd.WithAppID("wfacl-reload-caller"),
	)...)

	w.target = daprd.New(t, append(commonOpts,
		daprd.WithAppID("wfacl-reload-target"),
	)...)

	return []framework.Option{
		framework.WithProcesses(sen, w.kubeapi, w.oper, w.sched, w.place, w.caller, w.target),
	}
}

func (w *workflowaccesspolicies) Run(t *testing.T, ctx context.Context) {
	w.oper.WaitUntilRunning(t, ctx)
	w.place.WaitUntilRunning(t, ctx)
	w.sched.WaitUntilRunning(t, ctx)
	w.caller.WaitUntilRunning(t, ctx)
	w.target.WaitUntilRunning(t, ctx)

	callerRegistry := task.NewTaskRegistry()
	targetRegistry := task.NewTaskRegistry()

	require.NoError(t, callerRegistry.AddWorkflowN("CrossAppCall", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		err := ctx.CallChildWorkflow("TargetWF",
			task.WithChildWorkflowAppID(w.target.AppID())).
			Await(&output)
		if err != nil {
			return nil, fmt.Errorf("cross-app call failed: %w", err)
		}
		return output, nil
	}))

	require.NoError(t, targetRegistry.AddWorkflowN("TargetWF", func(ctx *task.WorkflowContext) (any, error) {
		return "target-ok", nil
	}))

	callerClient := client.NewTaskHubGrpcClient(w.caller.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, callerClient.StartWorkItemListener(ctx, callerRegistry))
	targetClient := client.NewTaskHubGrpcClient(w.target.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, targetClient.StartWorkItemListener(ctx, targetRegistry))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(w.caller.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
		assert.GreaterOrEqual(c, len(w.target.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	t.Run("no policies initially, cross-app workflow succeeds", func(t *testing.T) {
		id, err := callerClient.ScheduleNewWorkflow(ctx, "CrossAppCall")
		require.NoError(t, err)
		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(metadata))
		assert.Nil(t, metadata.GetFailureDetails())
	})

	t.Run("add deny policy via informer, cross-app workflow denied", func(t *testing.T) {
		// Add a policy scoped to the target that denies the caller.
		policy := &wfaclapi.WorkflowAccessPolicy{
			TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "WorkflowAccessPolicy"},
			ObjectMeta: metav1.ObjectMeta{Name: "deny-caller", Namespace: "default"},
			Scoped:     common.Scoped{Scopes: []string{"wfacl-reload-target"}},
			Spec: wfaclapi.WorkflowAccessPolicySpec{
				DefaultAction: wfaclapi.PolicyActionDeny,
				Rules: []wfaclapi.WorkflowAccessPolicyRule{{
					// Allow the target to run its own activities.
					Callers: []wfaclapi.WorkflowCaller{{AppID: "wfacl-reload-target"}},
					Operations: []wfaclapi.WorkflowOperationRule{{
						Type: wfaclapi.WorkflowOperationTypeActivity, Name: "*", Action: wfaclapi.PolicyActionAllow,
					}},
				}},
			},
		}
		w.pStore.Add(policy)
		w.kubeapi.Informer().Add(t, policy)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			id, err := callerClient.ScheduleNewWorkflow(ctx, "CrossAppCall")
			if !assert.NoError(c, err) {
				return
			}
			metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
			if !assert.NoError(c, err) {
				return
			}
			assert.NotNil(c, metadata.GetFailureDetails(),
				"cross-app call should be denied after policy is added")
		}, time.Second*20, time.Millisecond*500)
	})

	t.Run("modify policy to allow caller, cross-app workflow succeeds", func(t *testing.T) {
		policy := &wfaclapi.WorkflowAccessPolicy{
			TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "WorkflowAccessPolicy"},
			ObjectMeta: metav1.ObjectMeta{Name: "deny-caller", Namespace: "default"},
			Scoped:     common.Scoped{Scopes: []string{"wfacl-reload-target"}},
			Spec: wfaclapi.WorkflowAccessPolicySpec{
				DefaultAction: wfaclapi.PolicyActionDeny,
				Rules: []wfaclapi.WorkflowAccessPolicyRule{
					{
						Callers: []wfaclapi.WorkflowCaller{{AppID: "wfacl-reload-caller"}},
						Operations: []wfaclapi.WorkflowOperationRule{{
							Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow,
						}},
					},
					{
						// Target must be able to process its own workflows and activities.
						Callers: []wfaclapi.WorkflowCaller{{AppID: "wfacl-reload-target"}},
						Operations: []wfaclapi.WorkflowOperationRule{
							{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow},
							{Type: wfaclapi.WorkflowOperationTypeActivity, Name: "*", Action: wfaclapi.PolicyActionAllow},
						},
					},
				},
			},
		}
		w.pStore.Set(policy)
		w.kubeapi.Informer().Modify(t, policy)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			id, err := callerClient.ScheduleNewWorkflow(ctx, "CrossAppCall")
			if !assert.NoError(c, err) {
				return
			}
			metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
			if !assert.NoError(c, err) {
				return
			}
			assert.True(c, api.WorkflowMetadataIsComplete(metadata))
			assert.Nil(c, metadata.GetFailureDetails(),
				"cross-app call should succeed after policy is modified to allow caller")
		}, time.Second*20, time.Millisecond*500)
	})

	t.Run("delete policy, back to allow-all", func(t *testing.T) {
		policy := &wfaclapi.WorkflowAccessPolicy{
			TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "WorkflowAccessPolicy"},
			ObjectMeta: metav1.ObjectMeta{Name: "deny-caller", Namespace: "default"},
		}
		w.pStore.Set()
		w.kubeapi.Informer().Delete(t, policy)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			id, err := callerClient.ScheduleNewWorkflow(ctx, "CrossAppCall")
			if !assert.NoError(c, err) {
				return
			}
			metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
			if !assert.NoError(c, err) {
				return
			}
			assert.True(c, api.WorkflowMetadataIsComplete(metadata))
			assert.Nil(c, metadata.GetFailureDetails(),
				"cross-app call should succeed after all policies are deleted")
		}, time.Second*20, time.Millisecond*500)
	})
}
