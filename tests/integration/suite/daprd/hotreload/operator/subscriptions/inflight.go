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

package subscriptions

import (
	"context"
	"fmt"
	nethttp "net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	"github.com/dapr/dapr/pkg/operator/api"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/http/subscriber"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(inflight))
}

// inflight ensures in-flight messages are not lost when subscriptions
// are hot-reloaded.
type inflight struct {
	daprd         *daprd.Daprd
	sub           *subscriber.Subscriber
	operator      *operator.Operator
	inPublish     chan struct{}
	returnPublish chan struct{}
}

func (i *inflight) Setup(t *testing.T) []framework.Option {
	i.inPublish = make(chan struct{})
	i.returnPublish = make(chan struct{})
	i.sub = subscriber.New(t,
		subscriber.WithHandlerFunc("/a", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			close(i.inPublish)
			<-i.returnPublish
		}),
	)

	sentry := sentry.New(t)
	i.operator = operator.New(t,
		operator.WithSentry(sentry),
		operator.WithGetConfigurationFn(func(context.Context, *operatorv1.GetConfigurationRequest) (*operatorv1.GetConfigurationResponse, error) {
			return &operatorv1.GetConfigurationResponse{
				Configuration: []byte(
					`{"kind":"Configuration","apiVersion":"dapr.io/v1alpha1","metadata":{"name":"hotreloading"},"spec":{"features":[{"name":"HotReload","enabled":true}]}}`,
				),
			}, nil
		}),
	)

	i.daprd = daprd.New(t,
		daprd.WithAppPort(i.sub.Port()),
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("hotreloading"),
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors))),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(i.operator.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
	)

	i.operator.AddComponents(compapi.Component{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
		ObjectMeta: metav1.ObjectMeta{Name: "pubsub", Namespace: "default"},
		Spec:       compapi.ComponentSpec{Type: "pubsub.in-memory", Version: "v1"},
	})

	return []framework.Option{
		framework.WithProcesses(i.sub, sentry, i.operator, i.daprd),
	}
}

func (i *inflight) Run(t *testing.T, ctx context.Context) {
	i.daprd.WaitUntilRunning(t, ctx)

	assert.Len(t, i.daprd.GetMetaRegisteredComponents(t, ctx), 1)
	assert.Empty(t, i.daprd.GetMetaSubscriptions(t, ctx))

	sub := subapi.Subscription{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v2alpha1", Kind: "Subscription"},
		ObjectMeta: metav1.ObjectMeta{Name: "sub", Namespace: "default"},
		Spec: subapi.SubscriptionSpec{
			Pubsubname: "pubsub", Topic: "a",
			Routes: subapi.Routes{Default: "/a"},
		},
	}
	i.operator.AddSubscriptions(sub)
	i.operator.SubscriptionUpdateEvent(t, ctx, &api.SubscriptionUpdateEvent{
		Subscription: &sub,
		EventType:    operatorv1.ResourceEventType_CREATED,
	})

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, i.daprd.GetMetaSubscriptions(t, ctx), 1)
	}, time.Second*5, time.Millisecond*10)

	client := i.daprd.GRPCClient(t, ctx)
	_, err := client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "pubsub",
		Topic:      "a",
		Data:       []byte(`{"status":"completed"}`),
	})
	require.NoError(t, err)

	select {
	case <-i.inPublish:
	case <-time.After(time.Second * 5):
		assert.Fail(t, "did not receive publish event")
	}

	i.operator.SetSubscriptions()
	i.operator.SubscriptionUpdateEvent(t, ctx, &api.SubscriptionUpdateEvent{
		Subscription: &sub,
		EventType:    operatorv1.ResourceEventType_DELETED,
	})
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, i.daprd.GetMetaSubscriptions(t, ctx))
	}, time.Second*5, time.Millisecond*10)

	egressMetric := fmt.Sprintf("dapr_component_pubsub_egress_count|app_id:%s|component:pubsub|namespace:|success:true|topic:a", i.daprd.AppID())
	ingressMetric := fmt.Sprintf("dapr_component_pubsub_ingress_count|app_id:%s|component:pubsub|namespace:|process_status:success|status:success|topic:a", i.daprd.AppID())
	metrics := i.daprd.Metrics(t, ctx).All()
	assert.Equal(t, 1, int(metrics[egressMetric]))
	assert.Equal(t, 0, int(metrics[ingressMetric]))

	close(i.returnPublish)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics := i.daprd.Metrics(c, ctx).All()
		assert.Equal(c, 1, int(metrics[egressMetric]))
		assert.Equal(c, 1, int(metrics[ingressMetric]))
	}, time.Second*5, time.Millisecond*10)
}
