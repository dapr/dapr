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

package scopes

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
	suite.Register(new(addapp))
}

type addapp struct {
	sentry   *sentry.Sentry
	store    *store.Store
	kubeapi  *kubernetes.Kubernetes
	operator *operator.Operator
}

func (a *addapp) Setup(t *testing.T) []framework.Option {
	a.sentry = sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	a.store = store.New(metav1.GroupVersionKind{
		Group:   "dapr.io",
		Version: "v1alpha1",
		Kind:    "Component",
	})
	a.kubeapi = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			a.sentry.Port(),
		),
		kubernetes.WithClusterDaprComponentListFromStore(t, a.store),
	)

	a.operator = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(a.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(a.sentry.TrustAnchorsFile(t)),
	)

	return []framework.Option{
		framework.WithProcesses(a.sentry, a.kubeapi, a.operator),
	}
}

func (a *addapp) Run(t *testing.T, ctx context.Context) {
	a.sentry.WaitUntilRunning(t, ctx)
	a.operator.WaitUntilRunning(t, ctx)

	client := a.operator.Dial(t, ctx, a.sentry, "myapp")

	comp := &compapi.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "mycomponent", Namespace: "default", CreationTimestamp: metav1.Time{}},
		Spec: compapi.ComponentSpec{
			Type:         "state.redis",
			Version:      "v1",
			IgnoreErrors: false,
			Metadata: []common.NameValuePair{
				{Name: "connectionString", Value: common.DynamicValue{JSON: apiextv1.JSON{Raw: []byte(`"foobar"`)}}},
			},
		},
	}
	a.store.Add(comp)

	stream, err := client.ComponentUpdate(ctx, &operatorv1.ComponentUpdateRequest{Namespace: "default"})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, stream.CloseSend()) })

	event, err := stream.Recv()
	require.NoError(t, err)

	var gotComp compapi.Component
	require.NoError(t, json.Unmarshal(event.GetComponent(), &gotComp))
	assert.Equal(t, comp, &gotComp)
	assert.Equal(t, operatorv1.ResourceEventType_CREATED, event.GetType())
	assert.Equal(t, "CREATED", event.GetType().String())

	t.Run("Adding scope for another app ID should send update", func(t *testing.T) {
		newcomp := comp.DeepCopy()
		newcomp.Scopes = []string{"myapp", "app1"}
		a.store.Set(newcomp)
		a.kubeapi.Informer().Modify(t, newcomp)

		event, err := stream.Recv()
		require.NoError(t, err)

		var gotComp compapi.Component
		require.NoError(t, json.Unmarshal(event.GetComponent(), &gotComp))
		assert.Equal(t, newcomp, &gotComp)
		assert.Equal(t, operatorv1.ResourceEventType_UPDATED, event.GetType())
		assert.Equal(t, "UPDATED", event.GetType().String())
	})
}
