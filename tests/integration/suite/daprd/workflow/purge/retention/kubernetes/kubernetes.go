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
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
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

// cluster is a Kubernetes-mode control plane whose Configuration carries a
// workflow state retention policy.
type cluster struct {
	daprd   *daprd.Daprd
	place   *placement.Placement
	sched   *scheduler.Scheduler
	kubeapi *kubernetes.Kubernetes
	oper    *operator.Operator
}

func (c *cluster) setup(t *testing.T, policy *configapi.WorkflowStateRetentionPolicy) []framework.Option {
	sentrySvc := sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	c.kubeapi = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			sentrySvc.Port(),
		),
		kubernetes.WithDaprPods(t, "default", map[string]string{"myapp": "wfretention"}),
		kubernetes.WithClusterDaprConfigurationList(t, &configapi.ConfigurationList{
			Items: []configapi.Configuration{{
				TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Configuration"},
				ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "wfretention"},
				Spec: configapi.ConfigurationSpec{
					MTLSSpec: &configapi.MTLSSpec{
						ControlPlaneTrustDomain: "integration.test.dapr.io",
						SentryAddress:           sentrySvc.Address(),
					},
					WorkflowSpec: &configapi.WorkflowSpec{StateRetentionPolicy: policy},
				},
			}},
		}),
		kubernetes.WithClusterDaprComponentList(t, &compapi.ComponentList{
			Items: []compapi.Component{manifest.ActorInMemoryStateComponent("default", "mystore")},
		}),
	)

	c.oper = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(c.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(sentrySvc.TrustAnchorsFile(t)),
	)

	c.place = placement.New(t, placement.WithSentry(t, sentrySvc))

	c.sched = scheduler.New(t,
		scheduler.WithSentry(sentrySvc),
		scheduler.WithKubeconfig(c.kubeapi.KubeconfigPath(t)),
		scheduler.WithMode("kubernetes"),
		scheduler.WithID("dapr-scheduler-server-0"),
	)

	c.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("wfretention"),
		daprd.WithAppID("myapp"),
		daprd.WithNamespace("default"),
		daprd.WithSentry(t, sentrySvc),
		daprd.WithControlPlaneAddress(c.oper.Address()),
		daprd.WithPlacementAddresses(c.place.Address()),
		daprd.WithSchedulerAddresses(c.sched.Address()),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithControlPlaneTrustDomain("integration.test.dapr.io"),
	)

	return []framework.Option{
		framework.WithProcesses(sentrySvc, c.kubeapi, c.oper, c.sched, c.place, c.daprd),
	}
}

// client waits for the cluster to be running and starts a worker for reg.
func (c *cluster) client(t *testing.T, ctx context.Context, reg *dworkflow.Registry) *dworkflow.Client {
	c.oper.WaitUntilRunning(t, ctx)
	c.sched.WaitUntilRunning(t, ctx)
	c.place.WaitUntilRunning(t, ctx)
	c.daprd.WaitUntilRunning(t, ctx)

	client := dworkflow.NewClientWithLogger(c.daprd.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, client.StartWorker(ctx, reg))
	return client
}

// base tests that workflow state retention policy works in Kubernetes mode,
// validating that the CRD string duration fields are correctly parsed.
type base struct{ cluster }

func (b *base) Setup(t *testing.T) []framework.Option {
	return b.setup(t, &configapi.WorkflowStateRetentionPolicy{
		AnyTerminal: &metav1.Duration{Duration: time.Millisecond},
	})
}

func (b *base) Run(t *testing.T, ctx context.Context) {
	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("foo", func(ctx *dworkflow.WorkflowContext) (any, error) {
		require.NoError(t, ctx.CallActivity("abc").Await(nil))
		return nil, nil
	})
	reg.AddActivityN("abc", func(ctx dworkflow.ActivityContext) (any, error) {
		return nil, nil
	})
	client := b.client(t, ctx, reg)

	id, err := client.ScheduleWorkflow(ctx, "foo")
	require.NoError(t, err)

	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := client.ListInstanceIDs(ctx)
		require.NoError(c, err)
		assert.Empty(c, resp.InstanceIds)
	}, time.Second*10, time.Millisecond*10)
}
