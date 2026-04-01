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
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	grpcstatus "google.golang.org/grpc/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
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
	dtclient "github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(grpcapi))
}

// grpcapi tests workflow access policy enforcement through the gRPC API.
type grpcapi struct {
	daprd    *daprd.Daprd
	place    *placement.Placement
	sched    *scheduler.Scheduler
	kubeapi  *kubernetes.Kubernetes
	operator *operator.Operator
}

func (h *grpcapi) Setup(t *testing.T) []framework.Option {
	sen := sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	policyStore := store.New(metav1.GroupVersionKind{
		Group: "dapr.io", Version: "v1alpha1", Kind: "WorkflowAccessPolicy",
	})
	policyStore.Add(&wfaclapi.WorkflowAccessPolicy{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "WorkflowAccessPolicy"},
		ObjectMeta: metav1.ObjectMeta{Name: "grpcapi-test", Namespace: "default"},
		Scoped:     common.Scoped{Scopes: []string{}},
		Spec: wfaclapi.WorkflowAccessPolicySpec{
			DefaultAction: wfaclapi.PolicyActionDeny,
			Rules: []wfaclapi.WorkflowAccessPolicyRule{{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "grpcapi-app"}},
				Operations: []wfaclapi.WorkflowOperationRule{
					{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "AllowedWF", Action: wfaclapi.PolicyActionAllow},
					{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "DeniedWF", Action: wfaclapi.PolicyActionDeny},
				},
			}},
		},
	})

	boolTrue := true
	h.kubeapi = kubernetes.New(t,
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

	h.operator = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(h.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(sen.TrustAnchorsFile(t)),
	)

	h.place = placement.New(t, placement.WithSentry(t, sen))

	h.sched = scheduler.New(t,
		scheduler.WithSentry(sen),
		scheduler.WithKubeconfig(h.kubeapi.KubeconfigPath(t)),
		scheduler.WithMode("kubernetes"),
		scheduler.WithID("dapr-scheduler-server-0"),
	)

	h.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("daprsystem"),
		daprd.WithNamespace("default"),
		daprd.WithSentry(t, sen),
		daprd.WithControlPlaneAddress(h.operator.Address()),
		daprd.WithPlacementAddresses(h.place.Address()),
		daprd.WithSchedulerAddresses(h.sched.Address()),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithControlPlaneTrustDomain("integration.test.dapr.io"),
		daprd.WithAppID("grpcapi-app"),
	)

	return []framework.Option{
		framework.WithProcesses(sen, h.kubeapi, h.operator, h.sched, h.place, h.daprd),
	}
}

//nolint:staticcheck
func (h *grpcapi) Run(t *testing.T, ctx context.Context) {
	h.operator.WaitUntilRunning(t, ctx)
	h.place.WaitUntilRunning(t, ctx)
	h.sched.WaitUntilRunning(t, ctx)
	h.daprd.WaitUntilRunning(t, ctx)

	registry := task.NewTaskRegistry()

	require.NoError(t, registry.AddWorkflowN("AllowedWF", func(ctx *task.WorkflowContext) (any, error) {
		return "allowed-ok", nil
	}))

	require.NoError(t, registry.AddWorkflowN("DeniedWF", func(ctx *task.WorkflowContext) (any, error) {
		return "denied-should-not-reach", nil
	}))

	backendClient := dtclient.NewTaskHubGrpcClient(h.daprd.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, backendClient.StartWorkItemListener(ctx, registry))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(h.daprd.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	daprClient := runtimev1pb.NewDaprClient(h.daprd.GRPCConn(t, ctx))

	t.Run("gRPC start denied workflow returns PermissionDenied", func(t *testing.T) {
		_, err := daprClient.StartWorkflowAlpha1(ctx, &runtimev1pb.StartWorkflowRequest{
			WorkflowComponent: "dapr",
			WorkflowName:      "DeniedWF",
		})
		require.Error(t, err)
		st, ok := grpcstatus.FromError(err)
		require.True(t, ok, "expected gRPC status error")
		assert.Contains(t, st.Message(), "access denied by workflow access policy")
	})

	t.Run("gRPC start allowed workflow succeeds", func(t *testing.T) {
		resp, err := daprClient.StartWorkflowAlpha1(ctx, &runtimev1pb.StartWorkflowRequest{
			WorkflowComponent: "dapr",
			WorkflowName:      "AllowedWF",
		})
		require.NoError(t, err)
		assert.NotEmpty(t, resp.GetInstanceId())
	})

	t.Run("gRPC terminate exercises actor path", func(t *testing.T) {
		_, err := daprClient.TerminateWorkflowAlpha1(ctx, &runtimev1pb.TerminateWorkflowRequest{
			WorkflowComponent: "dapr",
			InstanceId:        "fake-instance",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "instance")
	})

	t.Run("gRPC purge exercises actor path", func(t *testing.T) {
		_, err := daprClient.PurgeWorkflowAlpha1(ctx, &runtimev1pb.PurgeWorkflowRequest{
			WorkflowComponent: "dapr",
			InstanceId:        "fake-instance",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "instance")
	})
}
