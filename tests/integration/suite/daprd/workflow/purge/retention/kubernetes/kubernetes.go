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

package kubernetes

import (
	"context"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/manifest"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(base))
}

// base tests that workflow state retention policy works in Kubernetes mode,
// validating that the CRD string duration fields are correctly parsed.
type base struct {
	daprd   *daprd.Daprd
	place   *placement.Placement
	sched   *scheduler.Scheduler
	kubeapi *kubernetes.Kubernetes
	oper    *operator.Operator
}

func (b *base) Setup(t *testing.T) []framework.Option {
	sentrySvc := sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	b.kubeapi = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			sentrySvc.Port(),
		),
		kubernetes.WithClusterDaprConfigurationList(t, &configapi.ConfigurationList{
			Items: []configapi.Configuration{{
				TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Configuration"},
				ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "wfretention"},
				Spec: configapi.ConfigurationSpec{
					MTLSSpec: &configapi.MTLSSpec{
						ControlPlaneTrustDomain: "integration.test.dapr.io",
						SentryAddress:           sentrySvc.Address(),
					},
					WorkflowSpec: &configapi.WorkflowSpec{
						StateRetentionPolicy: &configapi.WorkflowStateRetentionPolicy{
							AnyTerminal: &metav1.Duration{Duration: time.Millisecond},
						},
					},
				},
			}},
		}),
		kubernetes.WithClusterDaprComponentList(t, &compapi.ComponentList{
			Items: []compapi.Component{manifest.ActorInMemoryStateComponent("default", "mystore")},
		}),
	)

	b.oper = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(b.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(sentrySvc.TrustAnchorsFile(t)),
	)

	b.place = placement.New(t, placement.WithSentry(t, sentrySvc))

	b.sched = scheduler.New(t,
		scheduler.WithSentry(sentrySvc),
		scheduler.WithKubeconfig(b.kubeapi.KubeconfigPath(t)),
		scheduler.WithMode("kubernetes"),
		scheduler.WithID("dapr-scheduler-server-0"),
	)

	b.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("wfretention"),
		daprd.WithNamespace("default"),
		daprd.WithSentry(t, sentrySvc),
		daprd.WithControlPlaneAddress(b.oper.Address()),
		daprd.WithPlacementAddresses(b.place.Address()),
		daprd.WithSchedulerAddresses(b.sched.Address()),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithControlPlaneTrustDomain("integration.test.dapr.io"),
	)

	return []framework.Option{
		framework.WithProcesses(sentrySvc, b.kubeapi, b.oper, b.sched, b.place, b.daprd),
	}
}

func (b *base) Run(t *testing.T, ctx context.Context) {
	b.oper.WaitUntilRunning(t, ctx)
	b.sched.WaitUntilRunning(t, ctx)
	b.place.WaitUntilRunning(t, ctx)
	b.daprd.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("foo", func(ctx *dworkflow.WorkflowContext) (any, error) {
		require.NoError(t, ctx.CallActivity("abc").Await(nil))
		return nil, nil
	})
	reg.AddActivityN("abc", func(ctx dworkflow.ActivityContext) (any, error) {
		return nil, nil
	})

	client := dworkflow.NewClient(b.daprd.GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "foo")
	require.NoError(t, err)

	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := client.ListInstanceIDs(ctx)
		require.NoError(t, err)
		assert.Empty(c, resp.InstanceIds)
	}, time.Second*10, time.Millisecond*10)
}
