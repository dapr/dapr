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

package api

import (
	"context"
	"encoding/json"
	"testing"

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
	suite.Register(new(componentupdate))
}

// componentupdate tests the operator's ComponentUpdate API.
type componentupdate struct {
	sentry   *sentry.Sentry
	store    *store.Store
	kubeapi  *kubernetes.Kubernetes
	operator *operator.Operator
}

func (c *componentupdate) Setup(t *testing.T) []framework.Option {
	c.sentry = sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	c.store = store.New(metav1.GroupVersionKind{
		Group:   "dapr.io",
		Version: "v1alpha1",
		Kind:    "Component",
	})
	c.kubeapi = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			c.sentry.Port(),
		),
		kubernetes.WithClusterDaprComponentListFromStore(t, c.store),
	)

	c.operator = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(c.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(c.sentry.TrustAnchorsFile(t)),
	)

	return []framework.Option{
		framework.WithProcesses(c.kubeapi, c.sentry, c.operator),
	}
}

func (c *componentupdate) Run(t *testing.T, ctx context.Context) {
	c.sentry.WaitUntilRunning(t, ctx)
	c.operator.WaitUntilRunning(t, ctx)

	client := c.operator.Dial(t, ctx, "default", c.sentry)

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
		c.store.Add(comp)
		c.kubeapi.Informer().Add(t, comp)

		event, err := stream.Recv()
		require.NoError(t, err)
		assert.JSONEq(t, `{
"metadata": { "name": "mycomponent", "namespace": "default", "creationTimestamp": null },
"spec": {
"ignoreErrors": false, "initTimeout": "", "type": "state.redis", "version": "v1", "metadata":
[ { "name": "connectionString", "secretKeyRef": { "key": "", "name": "" }, "value": "foobar" } ] },
"auth": { "secretStore": "" }
}`, string(event.GetComponent()))
		assert.Equal(t, operatorv1.ResourceEventType_CREATED, event.GetType())
		assert.Equal(t, "CREATED", event.GetType().String())
	})

	t.Run("UPDATE", func(t *testing.T) {
		comp.Spec.Type = "state.inmemory"
		comp.Spec.Metadata = nil
		c.store.Set(comp)

		b, err := json.Marshal(comp)
		require.NoError(t, err)

		event, err := stream.Recv()
		require.NoError(t, err)
		assert.JSONEq(t, `{
"metadata": { "name": "mycomponent", "namespace": "default", "creationTimestamp": null },
"spec":{"ignoreErrors": false, "initTimeout": "", "type": "state.inmemory", "version": "v1", "metadata": null},
"auth": { "secretStore": "" }
}`, string(event.GetComponent()))
		assert.JSONEq(t, string(b), string(event.GetComponent()))
		assert.Equal(t, operatorv1.ResourceEventType_UPDATED, event.GetType())
		assert.Equal(t, "UPDATED", event.GetType().String())
	})

	t.Run("DELETE", func(t *testing.T) {
		c.store.Set()
		c.kubeapi.Informer().Delete(t, comp)

		b, err := json.Marshal(comp)
		require.NoError(t, err)

		event, err := stream.Recv()
		require.NoError(t, err)
		assert.JSONEq(t, `{
"metadata": { "name": "mycomponent", "namespace": "default", "creationTimestamp": null },
"spec":{"ignoreErrors": false, "initTimeout": "", "type": "state.inmemory", "version": "v1", "metadata": null},
"auth": { "secretStore": "" }
}`, string(event.GetComponent()))
		assert.JSONEq(t, string(b), string(event.GetComponent()))
		assert.Equal(t, operatorv1.ResourceEventType_DELETED, event.GetType())
		assert.Equal(t, "DELETED", event.GetType().String())
	})
}
