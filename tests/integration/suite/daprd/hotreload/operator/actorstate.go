/*
Copyright 2023 The Dapr Authors
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

package operator

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/operator/api"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	dtapi "github.com/dapr/durabletask-go/api"
	dtclient "github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(actorstate))
}

// actorstate ensures the actor state store can be hot reloaded through the
// operator: unmarking the component as the actor state store shuts down actor
// hosting in-process (workflow and actor state APIs error), and re-marking it
// re-enables hosting.
type actorstate struct {
	daprd    *daprd.Daprd
	operator *operator.Operator
}

func markedStore(marked bool) compapi.Component {
	comp := compapi.Component{
		TypeMeta:   metav1.TypeMeta{Kind: "Component", APIVersion: "dapr.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: "mystore", Namespace: "default"},
		Spec: compapi.ComponentSpec{
			Type: "state.in-memory", Version: "v1",
		},
	}
	if marked {
		comp.Spec.Metadata = []common.NameValuePair{{
			Name:  "actorStateStore",
			Value: common.DynamicValue{JSON: apiextv1.JSON{Raw: []byte(`"true"`)}},
		}}
	}
	return comp
}

func (a *actorstate) Setup(t *testing.T) []framework.Option {
	sen := sentry.New(t)

	a.operator = operator.New(t,
		operator.WithSentry(sen),
	)
	a.operator.SetComponents(markedStore(true))

	place := placement.New(t, placement.WithSentry(t, sen))
	sched := scheduler.New(t,
		scheduler.WithSentry(sen),
		// The scheduler ID must match the TLS cert DNS names issued by
		// Sentry.
		scheduler.WithID("dapr-scheduler-server-0"),
	)

	a.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithExecOptions(
			exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(sen.CABundle().X509.TrustAnchors)),
		),
		daprd.WithSentryAddress(sen.Address()),
		daprd.WithControlPlaneAddress(a.operator.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithScheduler(sched),
	)

	return []framework.Option{
		framework.WithProcesses(sen, a.operator, place, sched, a.daprd),
	}
}

func (a *actorstate) Run(t *testing.T, ctx context.Context) {
	a.daprd.WaitUntilRunning(t, ctx)

	comps := a.daprd.GetMetaRegisteredComponents(t, ctx)
	require.ElementsMatch(t, []*rtv1.RegisteredComponents{
		{
			Name: "mystore", Type: "state.in-memory", Version: "v1",
			Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "KEYS_LIKE", "ACTOR"},
		},
	}, comps)

	reg := task.NewTaskRegistry()
	require.NoError(t, reg.AddActivityN("SayHello", func(ctx task.ActivityContext) (any, error) {
		var name string
		if err := ctx.GetInput(&name); err != nil {
			return nil, err
		}
		return fmt.Sprintf("Hello, %s!", name), nil
	}))
	require.NoError(t, reg.AddWorkflowN("SingleActivity", func(ctx *task.WorkflowContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		var output string
		err := ctx.CallActivity("SayHello", task.WithActivityInput(input)).Await(&output)
		return output, err
	}))

	wfClient := dtclient.NewTaskHubGrpcClient(a.daprd.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, wfClient.StartWorkItemListener(ctx, reg))

	t.Run("workflows work with the actor state store", func(t *testing.T) {
		id, err := wfClient.ScheduleNewWorkflow(ctx, "SingleActivity", dtapi.WithInput("Dapr"), dtapi.WithInstanceID("beforeunmark"))
		require.NoError(t, err)
		meta, err := wfClient.WaitForWorkflowCompletion(ctx, id, dtapi.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, dtapi.WorkflowMetadataIsComplete(meta))
		assert.Equal(t, `"Hello, Dapr!"`, meta.GetOutput().GetValue())
	})

	t.Run("unmarking the actor state store shuts down actor hosting", func(t *testing.T) {
		unmarked := markedStore(false)
		a.operator.SetComponents(unmarked)
		a.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &unmarked, EventType: operatorv1.ResourceEventType_UPDATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			_, err := a.daprd.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
				WorkflowComponent: "dapr",
				WorkflowName:      "SingleActivity",
			})
			if !assert.Error(c, err) {
				return
			}
			s, ok := status.FromError(err)
			require.True(c, ok)
			assert.Equal(c, codes.Internal, s.Code())
			assert.Contains(c, err.Error(), "the state store is not configured to use the actor runtime")
		}, time.Second*20, time.Millisecond*10)

		// The component itself remains registered, just no longer as the
		// actor state store.
		comps := a.daprd.GetMetaRegisteredComponents(t, ctx)
		require.Len(t, comps, 1)
		assert.Equal(t, "mystore", comps[0].GetName())
	})

	t.Run("re-marking the actor state store re-enables actor hosting", func(t *testing.T) {
		marked := markedStore(true)
		a.operator.SetComponents(marked)
		a.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &marked, EventType: operatorv1.ResourceEventType_UPDATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			_, err := a.daprd.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
				WorkflowComponent: "dapr",
				WorkflowName:      "SingleActivity",
				InstanceId:        "afterremark",
				Input:             []byte(`"Dapr"`),
			})
			assert.NoError(c, err)
		}, time.Second*20, time.Millisecond*100)

		meta, err := wfClient.WaitForWorkflowCompletion(ctx, dtapi.InstanceID("afterremark"), dtapi.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, dtapi.WorkflowMetadataIsComplete(meta))
		assert.Equal(t, `"Hello, Dapr!"`, meta.GetOutput().GetValue())
	})
}
