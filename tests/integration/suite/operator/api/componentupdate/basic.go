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

package componentupdate

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
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

// basic tests the operator's ComponentUpdate API.
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
		Kind:    "Component",
	})
	b.kubeapi = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			b.sentry.Port(),
		),
		kubernetes.WithClusterDaprComponentListFromStore(t, b.store),
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

	stream, err := client.ComponentUpdate(ctx, &operatorv1.ComponentUpdateRequest{Namespace: "default"})
	require.NoError(t, err)

	comp := &compapi.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "mycomponent", Namespace: "default", CreationTimestamp: metav1.Time{}},
		Spec: compapi.ComponentSpec{
			Type:         "state.redis",
			Version:      "v1",
			IgnoreErrors: false,
			Metadata: []common.NameValuePair{
				{
					Name: "connectionString", Value: common.DynamicValue{
						JSON: apiextv1.JSON{Raw: []byte(`"foobar"`)},
					},
				},
			},
		},
	}

	t.Run("CREATE", func(t *testing.T) {
		b.store.Add(comp)
		b.kubeapi.Informer().Add(t, comp)

		event, err := stream.Recv()
		require.NoError(t, err)

		var gotComp compapi.Component
		require.NoError(t, json.Unmarshal(event.GetComponent(), &gotComp))
		assert.Equal(t, comp, &gotComp)
		assert.Equal(t, operatorv1.ResourceEventType_CREATED, event.GetType())
		assert.Equal(t, "CREATED", event.GetType().String())
	})

	t.Run("UPDATE", func(t *testing.T) {
		comp.Spec.Type = "state.inmemory"
		comp.Spec.Metadata = nil
		b.store.Set(comp)
		b.kubeapi.Informer().Modify(t, comp)

		b, err := json.Marshal(comp)
		require.NoError(t, err)

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			event, err := stream.Recv()
			//nolint:testifylint
			if !assert.NoError(c, err) {
				return
			}
			var gotComp compapi.Component
			require.NoError(t, json.Unmarshal(event.GetComponent(), &gotComp))
			assert.Equal(c, comp, &gotComp)
			assert.JSONEq(c, string(b), string(event.GetComponent()))
			assert.Equal(c, operatorv1.ResourceEventType_UPDATED, event.GetType())
			assert.Equal(c, "UPDATED", event.GetType().String())
		}, time.Second*10, time.Millisecond*10)
	})

	t.Run("DELETE", func(t *testing.T) {
		b.store.Set()
		b.kubeapi.Informer().Delete(t, comp)

		b, err := json.Marshal(comp)
		require.NoError(t, err)

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			event, err := stream.Recv()
			//nolint:testifylint
			if !assert.NoError(c, err) {
				return
			}
			var gotComp compapi.Component
			require.NoError(t, json.Unmarshal(event.GetComponent(), &gotComp))
			assert.Equal(t, comp, &gotComp)
			assert.JSONEq(c, string(b), string(event.GetComponent()))
			assert.Equal(c, operatorv1.ResourceEventType_DELETED, event.GetType())
			assert.Equal(c, "DELETED", event.GetType().String())
		}, time.Second*10, time.Millisecond*10)
	})
}
