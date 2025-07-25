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

package deadletter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(grpc))
}

type grpc struct {
	daprd    *daprd.Daprd
	kubeapi  *kubernetes.Kubernetes
	operator *operator.Operator
	app      *app.App
	inCh     chan *rtv1.TopicEventRequest
}

func (g *grpc) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))
	g.inCh = make(chan *rtv1.TopicEventRequest)
	g.app = app.New(t,
		app.WithOnTopicEventFn(func(ctx context.Context, in *rtv1.TopicEventRequest) (*rtv1.TopicEventResponse, error) {
			if in.GetTopic() == "a" {
				return nil, errors.New("my error")
			}
			g.inCh <- in
			return new(rtv1.TopicEventResponse), nil
		}),
	)

	g.kubeapi = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			sentry.Port(),
		),
		kubernetes.WithClusterDaprComponentList(t, &compapi.ComponentList{
			Items: []compapi.Component{{
				ObjectMeta: metav1.ObjectMeta{Name: "mypub", Namespace: "default"},
				Spec: compapi.ComponentSpec{
					Type: "pubsub.in-memory", Version: "v1",
				},
			}},
		}),
		kubernetes.WithClusterDaprSubscriptionListV2(t, &subapi.SubscriptionList{
			Items: []subapi.Subscription{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "mysub", Namespace: "default"},
					Spec: subapi.SubscriptionSpec{
						Pubsubname: "mypub",
						Topic:      "a",
						Routes: subapi.Routes{
							Default: "/a",
						},
						DeadLetterTopic: "mydead",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "anothersub", Namespace: "default"},
					Spec: subapi.SubscriptionSpec{
						Pubsubname: "mypub",
						Topic:      "mydead",
						Routes: subapi.Routes{
							Default: "/b",
						},
						DeadLetterTopic: "mydead",
					},
				},
			},
		}),
	)

	g.operator = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(g.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(sentry.TrustAnchorsFile(t)),
	)

	g.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(g.operator.Address()),
		daprd.WithAppPort(g.app.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithEnableMTLS(true),
		daprd.WithNamespace("default"),
		daprd.WithControlPlaneTrustDomain("integration.test.dapr.io"),
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors),
		)),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, g.app, g.kubeapi, g.operator, g.daprd),
	}
}

func (g *grpc) Run(t *testing.T, ctx context.Context) {
	g.operator.WaitUntilRunning(t, ctx)
	g.daprd.WaitUntilRunning(t, ctx)

	client := g.daprd.GRPCClient(t, ctx)

	_, err := client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub", Topic: "a", Data: []byte(`{"status": "completed"}`),
		Metadata: map[string]string{"foo": "bar"}, DataContentType: "application/json",
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(ctx, time.Second)
	t.Cleanup(cancel)
	select {
	case <-ctx.Done():
		assert.Fail(t, "timeout waiting for event")
	case in := <-g.inCh:
		assert.Equal(t, "mydead", in.GetTopic())
		assert.Equal(t, "/b", in.GetPath())
	}
}
