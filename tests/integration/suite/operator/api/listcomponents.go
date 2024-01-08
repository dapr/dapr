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
	"strings"
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
	operator "github.com/dapr/dapr/tests/integration/framework/process/operator"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(listcomponents))
}

// listcomponents tests the operator's ListCompontns API.
type listcomponents struct {
	sentry   *procsentry.Sentry
	kubeapi  *kubernetes.Kubernetes
	operator *operator.Operator

	comp1 *compapi.Component
	comp2 *compapi.Component
}

func (l *listcomponents) Setup(t *testing.T) []framework.Option {
	l.sentry = procsentry.New(t, procsentry.WithTrustDomain("integration.test.dapr.io"))

	l.comp1 = &compapi.Component{
		TypeMeta:   metav1.TypeMeta{Kind: "Component", APIVersion: "dapr.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: "mycomponent", Namespace: "default"},
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
	l.comp2 = &compapi.Component{
		TypeMeta:   metav1.TypeMeta{Kind: "Component", APIVersion: "dapr.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: "myothercomponent", Namespace: "default"},
		Spec: compapi.ComponentSpec{
			Type:    "state.inmemory",
			Version: "v1",
		},
	}

	// This component should not be listed as it is in a different namespace.
	comp3 := &compapi.Component{
		TypeMeta:   metav1.TypeMeta{Kind: "Component", APIVersion: "dapr.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "bar"},
		Spec: compapi.ComponentSpec{
			Type:    "state.inmemory",
			Version: "v1",
		},
	}

	l.kubeapi = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			l.sentry.Port(),
		),
		kubernetes.WithClusterDaprComponentList(t, &compapi.ComponentList{
			TypeMeta: metav1.TypeMeta{Kind: "ComponentList", APIVersion: "dapr.io/v1alpha1"},
			Items:    []compapi.Component{*l.comp1, *l.comp2, *comp3},
		}),
	)

	l.operator = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(l.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(l.sentry.TrustAnchorsFile(t)),
	)

	return []framework.Option{
		framework.WithProcesses(l.kubeapi, l.sentry, l.operator),
	}
}

func (l *listcomponents) Run(t *testing.T, ctx context.Context) {
	l.sentry.WaitUntilRunning(t, ctx)
	l.operator.WaitUntilRunning(t, ctx)

	client := l.operator.Dial(t, ctx, "default", l.sentry)

	t.Run("LIST", func(t *testing.T) {
		var resp *operatorv1.ListComponentResponse
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			var err error
			resp, err = client.ListComponents(ctx, &operatorv1.ListComponentsRequest{Namespace: "default"})
			require.NoError(t, err)
			assert.Len(c, resp.GetComponents(), 2)
		}, time.Second*20, time.Millisecond*100)

		b1, err := json.Marshal(l.comp1)
		require.NoError(t, err)
		b2, err := json.Marshal(l.comp2)
		require.NoError(t, err)

		if strings.Contains(string(resp.GetComponents()[0]), "mycomponent") {
			assert.JSONEq(t, string(b1), string(resp.GetComponents()[0]))
			assert.JSONEq(t, string(b2), string(resp.GetComponents()[1]))
		} else {
			assert.JSONEq(t, string(b1), string(resp.GetComponents()[1]))
			assert.JSONEq(t, string(b2), string(resp.GetComponents()[0]))
		}
	})
}
