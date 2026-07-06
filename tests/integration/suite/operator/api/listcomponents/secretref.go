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

package listcomponents

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes/store"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(secretref))
}

// secretref tests that the operator resolves component secretKeyRefs against
// the built-in Kubernetes secret store when serving components, and that it
// serves the *current* secret value on each call so a rotated secret is
// reflected in subsequent ListComponents responses. Sidecar hot reloading of
// components whose referenced secret changed depends on this contract.
type secretref struct {
	sentry      *procsentry.Sentry
	kubeapi     *kubernetes.Kubernetes
	operator    *operator.Operator
	secretStore *store.Store
}

func (s *secretref) Setup(t *testing.T) []framework.Option {
	s.sentry = procsentry.New(t, procsentry.WithTrustDomain("integration.test.dapr.io"))

	comp := compapi.Component{
		TypeMeta:   metav1.TypeMeta{Kind: "Component", APIVersion: "dapr.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: "mystore", Namespace: "default"},
		Spec: compapi.ComponentSpec{
			Type:    "state.redis",
			Version: "v1",
			Metadata: []common.NameValuePair{
				{
					Name: "redisPassword",
					SecretKeyRef: common.SecretKeyRef{
						Name: "redis-secret",
						Key:  "redis-password",
					},
				},
			},
		},
	}

	s.secretStore = store.New(metav1.GroupVersionKind{Version: "v1", Kind: "Secret"})
	s.secretStore.Add(&corev1.Secret{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{Name: "redis-secret", Namespace: "default"},
		Data:       map[string][]byte{"redis-password": []byte("hunter1")},
	})

	s.kubeapi = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			s.sentry.Port(),
		),
		kubernetes.WithClusterDaprComponentList(t, &compapi.ComponentList{
			TypeMeta: metav1.TypeMeta{Kind: "ComponentList", APIVersion: "dapr.io/v1alpha1"},
			Items:    []compapi.Component{comp},
		}),
		kubernetes.WithClusterSecretListFromStore(t, s.secretStore),
	)

	s.operator = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(s.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(s.sentry.TrustAnchorsFile(t)),
	)

	return []framework.Option{
		framework.WithProcesses(s.kubeapi, s.sentry, s.operator),
	}
}

func (s *secretref) Run(t *testing.T, ctx context.Context) {
	s.sentry.WaitUntilRunning(t, ctx)
	s.operator.WaitUntilRunning(t, ctx)

	client := s.operator.Dial(t, ctx, s.sentry, "myapp")

	// The operator resolves the secretKeyRef into the metadata value as a
	// JSON-encoded base64 string.
	expValue := func(plaintext string) []byte {
		b, err := json.Marshal(base64.StdEncoding.EncodeToString([]byte(plaintext)))
		require.NoError(t, err)
		return b
	}

	listPassword := func(c *assert.CollectT) []byte {
		resp, err := client.ListComponents(ctx, &operatorv1.ListComponentsRequest{Namespace: "default"})
		if !assert.NoError(c, err) || !assert.Len(c, resp.GetComponents(), 1) {
			return nil
		}
		var comp compapi.Component
		if !assert.NoError(c, json.Unmarshal(resp.GetComponents()[0], &comp)) ||
			!assert.Len(c, comp.Spec.Metadata, 1) {
			return nil
		}
		assert.Equal(c, "redis-secret", comp.Spec.Metadata[0].SecretKeyRef.Name)
		return comp.Spec.Metadata[0].Value.Raw
	}

	// List resolves the secretKeyRef against the kubernetes secret.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, expValue("hunter1"), listPassword(c))
	}, time.Second*20, time.Millisecond*10)

	// Rotating the secret is reflected in the next list.
	secret := corev1.Secret{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{Name: "redis-secret", Namespace: "default"},
		Data:       map[string][]byte{"redis-password": []byte("hunter2")},
	}
	s.secretStore.Set(&secret)
	s.kubeapi.Informer().Modify(t, &secret)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, expValue("hunter2"), listPassword(c))
	}, time.Second*20, time.Millisecond*10)
}
