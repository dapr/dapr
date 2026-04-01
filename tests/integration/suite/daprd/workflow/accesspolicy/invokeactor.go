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
	"google.golang.org/protobuf/proto"
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
	"github.com/dapr/durabletask-go/api/protos"
	dtclient "github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(invokeactor))
}

// invokeactor tests that a crafted InvokeActor call on the public gRPC API
// targeting a remote sidecar's workflow actors is denied by the workflow
// access policy on the target. This proves that bypassing the workflow SDK
// and using direct actor invocation does not circumvent ACL enforcement.
type invokeactor struct {
	attacker *daprd.Daprd
	target   *daprd.Daprd
	place    *placement.Placement
	sched    *scheduler.Scheduler
	kubeapi  *kubernetes.Kubernetes
	oper     *operator.Operator
}

func (ia *invokeactor) Setup(t *testing.T) []framework.Option {
	sen := sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	policyStore := store.New(metav1.GroupVersionKind{
		Group: "dapr.io", Version: "v1alpha1", Kind: "WorkflowAccessPolicy",
	})
	// Policy scoped to target. Only "legit-caller" is allowed.
	// "invokeactor-attacker" is NOT in any rule.
	policyStore.Add(&wfaclapi.WorkflowAccessPolicy{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "WorkflowAccessPolicy"},
		ObjectMeta: metav1.ObjectMeta{Name: "invokeactor-test", Namespace: "default"},
		Scoped:     common.Scoped{Scopes: []string{"invokeactor-target"}},
		Spec: wfaclapi.WorkflowAccessPolicySpec{
			DefaultAction: wfaclapi.PolicyActionDeny,
			Rules: []wfaclapi.WorkflowAccessPolicyRule{
				{
					Callers: []wfaclapi.WorkflowCaller{{AppID: "legit-caller"}},
					Operations: []wfaclapi.WorkflowOperationRule{
						{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow},
					},
				},
				{
					Callers: []wfaclapi.WorkflowCaller{{AppID: "invokeactor-target"}},
					Operations: []wfaclapi.WorkflowOperationRule{
						{Type: wfaclapi.WorkflowOperationTypeActivity, Name: "*", Action: wfaclapi.PolicyActionAllow},
					},
				},
			},
		},
	})

	boolTrue := true
	ia.kubeapi = kubernetes.New(t,
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

	ia.oper = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(ia.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(sen.TrustAnchorsFile(t)),
	)

	ia.place = placement.New(t, placement.WithSentry(t, sen))
	ia.sched = scheduler.New(t,
		scheduler.WithSentry(sen),
		scheduler.WithKubeconfig(ia.kubeapi.KubeconfigPath(t)),
		scheduler.WithMode("kubernetes"),
		scheduler.WithID("dapr-scheduler-server-0"),
	)

	commonOpts := []daprd.Option{
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("daprsystem"),
		daprd.WithNamespace("default"),
		daprd.WithSentry(t, sen),
		daprd.WithControlPlaneAddress(ia.oper.Address()),
		daprd.WithPlacementAddresses(ia.place.Address()),
		daprd.WithSchedulerAddresses(ia.sched.Address()),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithControlPlaneTrustDomain("integration.test.dapr.io"),
	}

	ia.target = daprd.New(t, append(commonOpts,
		daprd.WithAppID("invokeactor-target"),
	)...)

	ia.attacker = daprd.New(t, append(commonOpts,
		daprd.WithAppID("invokeactor-attacker"),
	)...)

	return []framework.Option{
		framework.WithProcesses(sen, ia.kubeapi, ia.oper, ia.sched, ia.place, ia.target, ia.attacker),
	}
}

func (ia *invokeactor) Run(t *testing.T, ctx context.Context) {
	ia.oper.WaitUntilRunning(t, ctx)
	ia.place.WaitUntilRunning(t, ctx)
	ia.sched.WaitUntilRunning(t, ctx)
	ia.target.WaitUntilRunning(t, ctx)
	ia.attacker.WaitUntilRunning(t, ctx)

	// Register a workflow on the target so the actor type is hosted.
	targetRegistry := task.NewTaskRegistry()
	require.NoError(t, targetRegistry.AddWorkflowN("SecretWF", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	}))
	targetClient := dtclient.NewTaskHubGrpcClient(ia.target.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, targetClient.StartWorkItemListener(ctx, targetRegistry))

	// Register a dummy workflow on attacker so actors are ready.
	attackerRegistry := task.NewTaskRegistry()
	require.NoError(t, attackerRegistry.AddWorkflowN("DummyWF", func(ctx *task.WorkflowContext) (any, error) {
		return "dummy", nil
	}))
	attackerClient := dtclient.NewTaskHubGrpcClient(ia.attacker.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, attackerClient.StartWorkItemListener(ctx, attackerRegistry))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(ia.target.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
		assert.GreaterOrEqual(c, len(ia.attacker.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	// Build a crafted CreateWorkflowInstanceRequest protobuf payload
	// that would normally be sent by the workflow engine via CallActor.
	craftedPayload, err := proto.Marshal(&protos.CreateWorkflowInstanceRequest{
		StartEvent: &protos.HistoryEvent{
			EventType: &protos.HistoryEvent_ExecutionStarted{
				ExecutionStarted: &protos.ExecutionStartedEvent{
					Name: "SecretWF",
					WorkflowInstance: &protos.WorkflowInstance{
						InstanceId: "crafted-attack-instance",
					},
				},
			},
		},
	})
	require.NoError(t, err)

	// The attacker uses InvokeActor on its OWN public gRPC API, targeting
	// the victim's workflow actor type. The router on the attacker will see
	// this is a remote actor (different appID), look up the address via
	// placement, and forward via internal CallActor to the target, which
	// runs the ACL check.
	attackerDaprClient := runtimev1pb.NewDaprClient(ia.attacker.GRPCConn(t, ctx))

	t.Run("crafted InvokeActor targeting remote workflow actor is denied", func(t *testing.T) {
		_, err := attackerDaprClient.InvokeActor(ctx, &runtimev1pb.InvokeActorRequest{
			ActorType: "dapr.internal.default.invokeactor-target.workflow",
			ActorId:   "crafted-attack-instance",
			Method:    "CreateWorkflowInstance",
			Data:      craftedPayload,
		})
		require.Error(t, err)
		// The target's ACL check denies the attacker because
		// invokeactor-attacker is not in any allow rule.
		assert.Contains(t, err.Error(), "access denied by workflow access policy")
	})

	t.Run("crafted InvokeActor for non-subject method is denied", func(t *testing.T) {
		// The attacker tries to invoke AddWorkflowEvent, a non-subject method.
		// The ACL falls back to IsCallerKnown, which rejects the attacker
		// since they have no allow rules.
		_, err := attackerDaprClient.InvokeActor(ctx, &runtimev1pb.InvokeActorRequest{
			ActorType: "dapr.internal.default.invokeactor-target.workflow",
			ActorId:   "some-instance",
			Method:    "AddWorkflowEvent",
			Data:      []byte{},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "access denied by workflow access policy")
	})
}
