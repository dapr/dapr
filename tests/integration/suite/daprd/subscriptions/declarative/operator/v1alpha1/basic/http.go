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

package basic

import (
	"context"
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v1alpha1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/http/subscriber"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(http))
}

type http struct {
	daprd    *daprd.Daprd
	kubeapi  *kubernetes.Kubernetes
	operator *operator.Operator
	sub      *subscriber.Subscriber
}

func (h *http) Setup(t *testing.T) []framework.Option {
	h.sub = subscriber.New(t, subscriber.WithRoutes("/a"))
	sentry := sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	h.kubeapi = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			sentry.Port(),
		),
		kubernetes.WithClusterDaprComponentList(t, &compapi.ComponentList{
			Items: []compapi.Component{{
				ObjectMeta: metav1.ObjectMeta{Name: "mypubsub", Namespace: "default"},
				Spec: compapi.ComponentSpec{
					Type: "pubsub.in-memory", Version: "v1",
				},
			}},
		}),
		kubernetes.WithClusterDaprSubscriptionList(t, &subapi.SubscriptionList{
			Items: []subapi.Subscription{{
				ObjectMeta: metav1.ObjectMeta{Name: "mysub", Namespace: "default"},
				Spec: subapi.SubscriptionSpec{
					Pubsubname: "mypubsub",
					Topic:      "a",
					Route:      "/a",
				},
			}},
		}),
	)

	h.operator = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(h.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(sentry.TrustAnchorsFile(t)),
	)

	h.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(h.operator.Address()),
		daprd.WithAppPort(h.sub.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithEnableMTLS(true),
		daprd.WithNamespace("default"),
		daprd.WithControlPlaneTrustDomain("integration.test.dapr.io"),
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors),
		)),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, h.sub, h.kubeapi, h.operator, h.daprd),
	}
}

func (h *http) Run(t *testing.T, ctx context.Context) {
	h.operator.WaitUntilRunning(t, ctx)
	h.daprd.WaitUntilRunning(t, ctx)

	h.sub.Publish(t, ctx, subscriber.PublishRequest{
		Daprd:      h.daprd,
		PubSubName: "mypubsub",
		Topic:      "a",
		Data:       `{"status": "completed"}`,
	})
	resp := h.sub.Receive(t, ctx)
	assert.Equal(t, "/a", resp.Route)
	assert.JSONEq(t, `{"status": "completed"}`, string(resp.Data()))
	assert.Equal(t, "1.0", resp.SpecVersion())
	assert.Equal(t, "mypubsub", resp.Extensions()["pubsubname"])
	assert.Equal(t, "a", resp.Extensions()["topic"])
	assert.Equal(t, "com.dapr.event.sent", resp.Type())
	assert.Equal(t, "text/plain", resp.DataContentType())

	h.sub.Publish(t, ctx, subscriber.PublishRequest{
		Daprd:           h.daprd,
		PubSubName:      "mypubsub",
		Topic:           "a",
		Data:            `{"status": "completed"}`,
		DataContentType: ptr.Of("application/json"),
	})
	resp = h.sub.Receive(t, ctx)
	assert.Equal(t, "/a", resp.Route)
	assert.JSONEq(t, `{"status": "completed"}`, string(resp.Data()))
	assert.Equal(t, "1.0", resp.SpecVersion())
	assert.Equal(t, "mypubsub", resp.Extensions()["pubsubname"])
	assert.Equal(t, "a", resp.Extensions()["topic"])
	assert.Equal(t, "com.dapr.event.sent", resp.Type())
	assert.Equal(t, "application/json", resp.DataContentType())

	h.sub.Publish(t, ctx, subscriber.PublishRequest{
		Daprd:           h.daprd,
		PubSubName:      "mypubsub",
		Topic:           "a",
		Data:            `{"status": "completed"}`,
		DataContentType: ptr.Of("foo/bar"),
	})
	resp = h.sub.Receive(t, ctx)
	assert.Equal(t, "/a", resp.Route)
	assert.JSONEq(t, `{"status": "completed"}`, string(resp.Data()))
	assert.Equal(t, "1.0", resp.SpecVersion())
	assert.Equal(t, "mypubsub", resp.Extensions()["pubsubname"])
	assert.Equal(t, "a", resp.Extensions()["topic"])
	assert.Equal(t, "com.dapr.event.sent", resp.Type())
	assert.Equal(t, "foo/bar", resp.DataContentType())

	h.sub.Publish(t, ctx, subscriber.PublishRequest{
		Daprd:      h.daprd,
		PubSubName: "mypubsub",
		Topic:      "a",
		Data:       "foo",
	})
	resp = h.sub.Receive(t, ctx)
	assert.Equal(t, "/a", resp.Route)
	assert.Equal(t, "foo", string(resp.Data()))
	assert.Equal(t, "1.0", resp.SpecVersion())
	assert.Equal(t, "mypubsub", resp.Extensions()["pubsubname"])
	assert.Equal(t, "a", resp.Extensions()["topic"])
	assert.Equal(t, "com.dapr.event.sent", resp.Type())
	assert.Equal(t, "text/plain", resp.DataContentType())
}
