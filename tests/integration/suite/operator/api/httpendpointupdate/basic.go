/*
Copyright 2025 The Dapr Authors
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

package httpendpointupdate

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	httpendapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes/store"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(basic))
}

// basic tests the operator's HTTPEndpointUpdate API.
type basic struct {
	sentry   *sentry.Sentry
	store    *store.Store
	kubeapi  *kubernetes.Kubernetes
	operator *operator.Operator
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	b.sentry = sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	b.store = store.New(metav1.GroupVersionKind{
		Group:   "dapr.io",
		Version: "v1alpha1",
		Kind:    "HTTPEndpoint",
	})
	b.kubeapi = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			b.sentry.Port(),
		),
		kubernetes.WithClusterDaprHTTPEndpointListFromStore(t, b.store),
	)

	b.operator = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(b.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(b.sentry.TrustAnchorsFile(t)),
	)

	return []framework.Option{
		framework.WithProcesses(b.kubeapi, b.sentry, b.operator),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	b.sentry.WaitUntilRunning(t, ctx)
	b.operator.WaitUntilRunning(t, ctx)

	client := b.operator.Dial(t, ctx, b.sentry, "myapp")

	stream, err := client.HTTPEndpointUpdate(ctx, &operatorv1.HTTPEndpointUpdateRequest{Namespace: "default"})
	require.NoError(t, err)

	endpoint := &httpendapi.HTTPEndpoint{
		ObjectMeta: metav1.ObjectMeta{Name: "myendpoint", Namespace: "default", CreationTimestamp: metav1.Time{}},
		Spec: httpendapi.HTTPEndpointSpec{
			BaseURL: "http://localhost:1234",
		},
	}

	t.Run("CREATE", func(t *testing.T) {
		b.store.Add(endpoint)
		b.kubeapi.Informer().Add(t, endpoint)

		event, err := stream.Recv()
		require.NoError(t, err)

		var gotEndpoint httpendapi.HTTPEndpoint
		require.NoError(t, json.Unmarshal(event.GetHttpEndpoints(), &gotEndpoint))
		assert.Equal(t, endpoint, &gotEndpoint)
		assert.Equal(t, operatorv1.ResourceEventType_CREATED, event.GetType())
		assert.Equal(t, "CREATED", event.GetType().String())
	})

	t.Run("UPDATE", func(t *testing.T) {
		endpoint.Spec.BaseURL = "http://localhost:5678"
		b.store.Set(endpoint)
		b.kubeapi.Informer().Modify(t, endpoint)

		endpointB, err := json.Marshal(endpoint)
		require.NoError(t, err)

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			event, err := stream.Recv()
			if !assert.NoError(c, err) {
				return
			}
			var gotEndpoint httpendapi.HTTPEndpoint
			if !assert.NoError(c, json.Unmarshal(event.GetHttpEndpoints(), &gotEndpoint)) {
				return
			}
			assert.Equal(c, endpoint, &gotEndpoint)
			assert.JSONEq(c, string(endpointB), string(event.GetHttpEndpoints()))
			assert.Equal(c, operatorv1.ResourceEventType_UPDATED, event.GetType())
			assert.Equal(c, "UPDATED", event.GetType().String())
		}, time.Second*10, time.Millisecond*10)
	})

	t.Run("DELETE", func(t *testing.T) {
		b.store.Set()
		b.kubeapi.Informer().Delete(t, endpoint)

		endpointB, err := json.Marshal(endpoint)
		require.NoError(t, err)

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			event, err := stream.Recv()
			if !assert.NoError(c, err) {
				return
			}
			var gotEndpoint httpendapi.HTTPEndpoint
			if !assert.NoError(c, json.Unmarshal(event.GetHttpEndpoints(), &gotEndpoint)) {
				return
			}
			assert.Equal(c, endpoint, &gotEndpoint)
			assert.JSONEq(c, string(endpointB), string(event.GetHttpEndpoints()))
			assert.Equal(c, operatorv1.ResourceEventType_DELETED, event.GetType())
			assert.Equal(c, "DELETED", event.GetType().String())
		}, time.Second*10, time.Millisecond*10)
	})
}
