/*
Copyright 2024 The Dapr Authors
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
	"net/http"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/manifest"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(namespace))
}

type namespace struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
	placement *placement.Placement
	kubeapi   *kubernetes.Kubernetes
}

func (n *namespace) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t)

	app := app.New(t,
		app.WithConfig(`{"entities": ["myactortype"]}`),
		app.WithHandlerFunc("/actors/myactortype/myactorid/method/foo", func(http.ResponseWriter, *http.Request) {}),
	)

	n.kubeapi = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t, spiffeid.RequireTrustDomainFromString("localhost"), "default", sentry.Port()),
		kubernetes.WithClusterDaprConfigurationList(t, &configapi.ConfigurationList{
			Items: []configapi.Configuration{},
		}),
		kubernetes.WithClusterDaprComponentList(t, &compapi.ComponentList{
			Items: []compapi.Component{manifest.ActorInMemoryStateComponent("default", "foo")},
		}),
		kubernetes.WithClusterNamespaceList(t, &corev1.NamespaceList{
			Items: []corev1.Namespace{{
				TypeMeta:   metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"},
				ObjectMeta: metav1.ObjectMeta{Name: "default"},
			}},
		}),
	)

	n.scheduler = scheduler.New(t,
		scheduler.WithSentry(sentry),
		scheduler.WithKubeconfig(n.kubeapi.KubeconfigPath(t)),
		scheduler.WithMode("kubernetes"),
		scheduler.WithID("dapr-scheduler-server-0"),
	)

	operator := operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(n.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(sentry.TrustAnchorsFile(t)),
	)

	n.placement = placement.New(t, placement.WithSentry(t, sentry))

	n.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithNamespace("default"),
		daprd.WithSentry(t, sentry),
		daprd.WithSchedulerAddresses(n.scheduler.Address()),
		daprd.WithAppPort(app.Port()),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithControlPlaneAddress(operator.Address()),
		daprd.WithPlacementAddresses(n.placement.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(app, sentry, n.kubeapi, n.scheduler, n.placement, operator, n.daprd),
	}
}

func (n *namespace) Run(t *testing.T, ctx context.Context) {
	n.daprd.WaitUntilRunning(t, ctx)
	n.scheduler.WaitUntilRunning(t, ctx)
	n.placement.WaitUntilRunning(t, ctx)

	client := n.daprd.GRPCClient(t, ctx)
	_, err := client.ScheduleJobAlpha1(ctx, &rtv1.ScheduleJobRequest{
		Job: &rtv1.Job{Name: "test", Schedule: ptr.Of("@daily")},
	})
	require.NoError(t, err)

	_, err = client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		Name:      "test",
		DueTime:   "1000s",
		ActorType: "myactortype",
		ActorId:   "myactorid",
	})
	require.NoError(t, err)

	etcdClient := n.scheduler.ETCDClient(t, ctx).KV
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := etcdClient.Get(ctx, "dapr/jobs/", clientv3.WithPrefix())
		assert.NoError(c, err)
		assert.Len(c, resp.Kvs, 2)
	}, time.Second*20, 10*time.Millisecond)

	n.kubeapi.Informer().DeleteWait(t, ctx, &corev1.Namespace{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
		ObjectMeta: metav1.ObjectMeta{Name: "default"},
	})

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := etcdClient.Get(ctx, "dapr/jobs/", clientv3.WithPrefix())
		if assert.NoError(c, err) {
			assert.Empty(c, resp.Kvs)
		}
	}, time.Second*10, 10*time.Millisecond)
}
