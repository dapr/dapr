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
	suite.Register(new(specificity))
}

// specificity tests the most-specific-rule-wins behavior end-to-end.
// Policy: deny *, allow Process*, deny ProcessSecret.
type specificity struct {
	daprd0   *daprd.Daprd
	daprd1   *daprd.Daprd
	place    *placement.Placement
	sched    *scheduler.Scheduler
	kubeapi  *kubernetes.Kubernetes
	operator *operator.Operator
}

func (s *specificity) Setup(t *testing.T) []framework.Option {
	sen := sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	policyStore := store.New(metav1.GroupVersionKind{
		Group: "dapr.io", Version: "v1alpha1", Kind: "WorkflowAccessPolicy",
	})
	policyStore.Add(&wfaclapi.WorkflowAccessPolicy{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "WorkflowAccessPolicy"},
		ObjectMeta: metav1.ObjectMeta{Name: "specificity-test", Namespace: "default"},
		Scoped:     common.Scoped{Scopes: []string{"spec-target"}},
		Spec: wfaclapi.WorkflowAccessPolicySpec{
			DefaultAction: wfaclapi.PolicyActionDeny,
			Rules: []wfaclapi.WorkflowAccessPolicyRule{{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "spec-caller"}},
				Operations: []wfaclapi.WorkflowOperationRule{
					{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionDeny},
					{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "Process*", Action: wfaclapi.PolicyActionAllow},
					{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "ProcessSecret", Action: wfaclapi.PolicyActionDeny},
				},
			}},
		},
	})

	boolTrue := true
	s.kubeapi = kubernetes.New(t,
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

	s.operator = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(s.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(sen.TrustAnchorsFile(t)),
	)

	s.place = placement.New(t, placement.WithSentry(t, sen))

	s.sched = scheduler.New(t,
		scheduler.WithSentry(sen),
		scheduler.WithKubeconfig(s.kubeapi.KubeconfigPath(t)),
		scheduler.WithMode("kubernetes"),
		scheduler.WithID("dapr-scheduler-server-0"),
	)

	commonOpts := []daprd.Option{
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("daprsystem"),
		daprd.WithNamespace("default"),
		daprd.WithSentry(t, sen),
		daprd.WithControlPlaneAddress(s.operator.Address()),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithSchedulerAddresses(s.sched.Address()),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithControlPlaneTrustDomain("integration.test.dapr.io"),
	}

	s.daprd0 = daprd.New(t, append(commonOpts,
		daprd.WithAppID("spec-caller"),
	)...)

	s.daprd1 = daprd.New(t, append(commonOpts,
		daprd.WithAppID("spec-target"),
	)...)

	return []framework.Option{
		framework.WithProcesses(sen, s.kubeapi, s.operator, s.sched, s.place, s.daprd0, s.daprd1),
	}
}

func (s *specificity) Run(t *testing.T, ctx context.Context) {
	s.operator.WaitUntilRunning(t, ctx)
	s.place.WaitUntilRunning(t, ctx)
	s.sched.WaitUntilRunning(t, ctx)
	s.daprd0.WaitUntilRunning(t, ctx)
	s.daprd1.WaitUntilRunning(t, ctx)

	registry0 := task.NewTaskRegistry()
	registry1 := task.NewTaskRegistry()

	targetAppID := s.daprd1.AppID()

	for _, wfName := range []string{"ProcessOrder", "ProcessSecret", "CancelOrder"} {
		name := wfName
		require.NoError(t, registry0.AddWorkflowN("Test_"+name, func(ctx *task.WorkflowContext) (any, error) {
			var output string
			err := ctx.CallChildWorkflow(name,
				task.WithChildWorkflowAppID(targetAppID)).
				Await(&output)
			if err != nil {
				return nil, fmt.Errorf("sub-orchestrator %s failed: %w", name, err)
			}
			return output, nil
		}))
	}

	for _, wfName := range []string{"ProcessOrder", "ProcessSecret", "CancelOrder"} {
		name := wfName
		require.NoError(t, registry1.AddWorkflowN(name, func(ctx *task.WorkflowContext) (any, error) {
			return "completed-" + name, nil
		}))
	}

	client0 := client.NewTaskHubGrpcClient(s.daprd0.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, client0.StartWorkItemListener(ctx, registry0))
	client1 := client.NewTaskHubGrpcClient(s.daprd1.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, client1.StartWorkItemListener(ctx, registry1))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(s.daprd0.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
		assert.GreaterOrEqual(c, len(s.daprd1.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	t.Run("ProcessOrder allowed (Process* matches, more specific than *)", func(t *testing.T) {
		id, err := client0.ScheduleNewWorkflow(ctx, "Test_ProcessOrder")
		require.NoError(t, err)
		metadata, err := client0.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(metadata))
	})

	t.Run("ProcessSecret denied (exact deny beats Process* allow)", func(t *testing.T) {
		id, err := client0.ScheduleNewWorkflow(ctx, "Test_ProcessSecret")
		require.NoError(t, err)

		metadata, err := client0.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails())
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(), "denied by workflow access policy")
	})

	t.Run("CancelOrder denied (only * matches, which is deny)", func(t *testing.T) {
		id, err := client0.ScheduleNewWorkflow(ctx, "Test_CancelOrder")
		require.NoError(t, err)

		metadata, err := client0.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails())
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(), "denied by workflow access policy")
	})
}
